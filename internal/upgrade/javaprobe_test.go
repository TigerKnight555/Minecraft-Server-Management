package upgrade

import "testing"

func TestParseJavaMajor(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		want int
	}{
		{
			name: "java 21 (der Stand, mit dem 26.2 scheiterte)",
			env:  []string{"TYPE=fabric", "JAVA_VERSION=jdk-21.0.8+9", "MEMORY=12G"},
			want: 21,
		},
		{
			name: "java 25 (das Image, mit dem es lief)",
			env:  []string{"JAVA_VERSION=jdk-25.0.1+9"},
			want: 25,
		},
		{
			name: "kleingeschriebener Schlüssel",
			env:  []string{"java_version=jdk-17.0.1+12"},
			want: 17,
		},
		{
			name: "kein Java-Eintrag -> unbekannt",
			env:  []string{"TYPE=fabric", "MEMORY=12G"},
			want: 0,
		},
		{
			name: "unlesbarer Wert -> unbekannt",
			env:  []string{"JAVA_VERSION=irgendwas"},
			want: 0,
		},
		{
			name: "leere Umgebung",
			env:  nil,
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseJavaMajor(tc.env); got != tc.want {
				t.Errorf("ParseJavaMajor = %d, want %d", got, tc.want)
			}
		})
	}
}
