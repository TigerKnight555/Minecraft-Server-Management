package upgrade

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
)

// Inspector liefert Container-Details inkl. Umgebung
// (dockerclient.Client erfüllt das).
type Inspector interface {
	InspectContainer(ctx context.Context, id string) (collector.ContainerDetail, error)
}

// Das itzg-Image setzt die Laufzeit-Java-Version in der Container-Umgebung:
//
//	JAVA_VERSION=jdk-21.0.8+9   bzw.   JAVA_VERSION=jdk-25.0.1+9
//
// Das ist verlässlicher als die Startzeile im Log, die nach längerer Laufzeit
// aus dem Tail-Fenster rutscht.
var javaEnvRe = regexp.MustCompile(`(?i)^JAVA_VERSION=\D*(\d+)`)

// NewImageJavaProbe liest die Java-Hauptversion des Minecraft-Containers.
// 0 = unbekannt — dann blockiert der Guard bewusst nicht.
func NewImageJavaProbe(insp Inspector, containers resolver, mcName string) func(context.Context) int {
	return func(ctx context.Context) int {
		if insp == nil {
			return 0
		}
		id := mcName
		for _, c := range containers.Containers() {
			if c.Name == mcName {
				id = c.ID
				break
			}
		}
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		det, err := insp.InspectContainer(ctx, id)
		if err != nil {
			return 0
		}
		return ParseJavaMajor(det.Env)
	}
}

// ParseJavaMajor zieht die Java-Hauptversion aus einer Container-Umgebung.
func ParseJavaMajor(env []string) int {
	for _, e := range env {
		if !strings.HasPrefix(strings.ToUpper(e), "JAVA_VERSION=") {
			continue
		}
		if m := javaEnvRe.FindStringSubmatch(e); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n
			}
		}
	}
	return 0
}
