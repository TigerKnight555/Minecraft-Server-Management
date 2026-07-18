// MSM — Minecraft Server Management dashboard.
//
// Phase 1 (Sichtbarkeit) + Phase 2 (Kontrolle): Dashboard, Log-Streaming,
// RCON-Konsole, Container-Aktionen, Routinen-Scheduler, Login.
// Konfiguration via Flags und Environment:
//
//	MSM_ADDR                listen address              (default :8080)
//	MSM_DOCKER_HOST         socket proxy base URL       (default http://socket-proxy:2375)
//	MSM_DB_PATH             sqlite file                 (default /data/msm.db)
//	MSM_QUERY_ADDR          minecraft query host:port   (default mc-fabric:25565)
//	MSM_RCON_ADDR           minecraft rcon host:port    (default mc-fabric:25575)
//	MSM_RCON_PASSWORD       rcon password               (no default; rcon disabled without it)
//	MSM_HOST_PROC           mounted host /proc          (default /host/proc)
//	MSM_NAS_PATH            NAS mountpoint to check     (empty disables the check)
//	MSM_PING_TARGETS        comma separated             (default 1.1.1.1,9.9.9.9)
//	MSM_ADMIN_PASSWORD_HASH argon2id hash for login     (empty = kein Login! nur dev)
//	MSM_MANAGED_CONTAINERS  comma separated allowlist for start/stop/restart
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/api"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/auth"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/dockerclient"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/hostmetrics"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mcquery"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mcrcon"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mcstatus"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mock"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/modrinth"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/netcheck"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/scheduler"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/storage"
	"github.com/TigerKnight555/Minecraft-Server-Management/web"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	var (
		addr        = flag.String("addr", envOr("MSM_ADDR", ":8080"), "listen address")
		mockMode    = flag.Bool("mock", false, "run with fake data sources (no docker/minecraft needed)")
		healthcheck = flag.Bool("healthcheck", false, "probe the running instance and exit (for docker HEALTHCHECK)")
		hashPass    = flag.String("hash-password", "", "print the argon2id hash for the given password and exit")
	)
	flag.Parse()

	if *hashPass != "" {
		hash, err := auth.HashPassword(*hashPass)
		if err != nil {
			fmt.Fprintln(os.Stderr, "hash failed:", err)
			os.Exit(1)
		}
		fmt.Println(hash)
		return
	}
	if *healthcheck {
		os.Exit(runHealthcheck(*addr))
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var (
		docker      collector.DockerClient
		controller  collector.ContainerController
		mc          collector.MCStatusSource
		rcon        collector.RCONClient
		host        collector.HostMetricsSource
		wan         collector.WANChecker
		store       collector.Store
		admin       api.AdminStore
		modAPI      mods.ModrinthAPI
		modProfiles []mods.Profile
	)

	if *mockMode {
		log.Info("running in MOCK mode — all data is fake")
		md := mock.NewDocker()
		ms := mock.NewStore()
		docker, controller = md, md
		mc, rcon, host, wan = mock.NewMC(), mock.NewRCON(), mock.NewHost(), mock.NewWAN()
		store, admin = ms, ms
		modAPI = mock.NewModrinth()
		profiles, err := mock.CreateFakeProfiles(filepath.Join(os.TempDir(), "msm-mock"))
		if err != nil {
			log.Error("mock profiles failed", "err", err)
			os.Exit(1)
		}
		modProfiles = profiles
	} else {
		dc := dockerclient.New(envOr("MSM_DOCKER_HOST", "http://socket-proxy:2375"))
		docker, controller = dc, dc
		host = hostmetrics.New(envOr("MSM_HOST_PROC", "/host/proc"), "/", os.Getenv("MSM_NAS_PATH"))
		wan = netcheck.New(strings.Split(envOr("MSM_PING_TARGETS", "1.1.1.1,9.9.9.9"), ","), envOr("MSM_HOST_PROC", "/host/proc"))

		query := mcquery.New(envOr("MSM_QUERY_ADDR", "mc-fabric:25565"))
		if pw := os.Getenv("MSM_RCON_PASSWORD"); pw != "" {
			rcon = mcrcon.New(envOr("MSM_RCON_ADDR", "mc-fabric:25575"), pw)
		} else {
			log.Warn("MSM_RCON_PASSWORD not set — rcon console and TPS disabled")
		}
		mc = mcstatus.New(query, rcon)

		dbPath := envOr("MSM_DB_PATH", "/data/msm.db")
		sq, err := storage.Open(dbPath)
		if err != nil {
			log.Error("open sqlite failed", "path", dbPath, "err", err)
			os.Exit(1)
		}
		defer sq.Close()
		store, admin = sq, sq
		go pruneLoop(ctx, sq, log)

		modAPI = modrinth.New()
		serverMods := envOr("MSM_SERVER_MODS_DIR", "/mc/mods")
		clientPack := envOr("MSM_CLIENT_PACK_DIR", "/mc/client-pack")
		modProfiles = []mods.Profile{
			{Name: "server", Dirs: map[string]string{"mods": serverMods}},
			{Name: "client", Dirs: map[string]string{
				"mods":          filepath.Join(clientPack, "mods"),
				"shaderpacks":   filepath.Join(clientPack, "shaderpacks"),
				"resourcepacks": filepath.Join(clientPack, "resourcepacks"),
			}},
		}
	}

	coll := collector.New(collector.Config{
		MCContainerName: envOr("MSM_MC_CONTAINER", "mc-fabric"),
	}, docker, mc, host, wan, store, log)
	go coll.Run(ctx)

	sched := scheduler.New(admin.(scheduler.RoutineStore), rcon, controller, coll, log)
	if err := sched.Start(ctx); err != nil {
		log.Error("scheduler start failed", "err", err)
		os.Exit(1)
	}

	loader := envOr("MSM_LOADER", "fabric")
	modmgr := mods.NewManager(modAPI, loader, modProfiles)
	watcher := mods.NewWatcher(modAPI, modmgr, loader)
	go watcher.Run(ctx, func() string {
		if v := coll.MCVersion(); v != "" {
			return v
		}
		return os.Getenv("MC_VERSION")
	})

	passwordHash := os.Getenv("MSM_ADMIN_PASSWORD_HASH")
	if passwordHash == "" && !*mockMode {
		log.Warn("MSM_ADMIN_PASSWORD_HASH not set — dashboard runs WITHOUT login (nur für Entwicklung akzeptabel)")
	}
	authmgr := auth.NewManager(passwordHash, log)

	frontend, err := web.Dist()
	if err != nil {
		log.Error("embedded frontend unavailable", "err", err)
		os.Exit(1)
	}

	managed := strings.Split(envOr("MSM_MANAGED_CONTAINERS", "mc-fabric"), ",")
	srv := &http.Server{
		Addr: *addr,
		Handler: api.New(api.Deps{
			Collector:         coll,
			Docker:            docker,
			Controller:        controller,
			RCON:              rcon,
			Store:             store,
			Admin:             admin,
			Scheduler:         sched,
			Auth:              authmgr,
			ModManager:        modmgr,
			Watcher:           watcher,
			MCContainer:       envOr("MSM_MC_CONTAINER", "mc-fabric"),
			FallbackMCVersion: os.Getenv("MC_VERSION"),
			Managed:           managed,
			Frontend:          frontend,
			Log:               log,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	log.Info("msm listening", "addr", *addr, "mock", *mockMode, "login", authmgr.Enabled())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("http server failed", "err", err)
		os.Exit(1)
	}
}

// runHealthcheck probes /healthz of the running instance; used as docker
// HEALTHCHECK since the distroless image has no shell or curl.
func runHealthcheck(addr string) int {
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func pruneLoop(ctx context.Context, s *storage.SQLite, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.Prune(ctx); err != nil {
				log.Error("prune failed", "err", err)
			}
		}
	}
}
