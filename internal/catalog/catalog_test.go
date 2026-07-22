package catalog

import (
	"strings"
	"testing"
)

// Every shipped template is parsed and validated here, so a broken one fails in
// CI rather than at a user's first click on Install.
func TestEmbeddedTemplatesAreValid(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if len(c.All()) == 0 {
		t.Fatal("catalogue is empty")
	}

	for _, tmpl := range c.All() {
		secrets, err := tmpl.GenerateSecrets()
		if err != nil {
			t.Errorf("%s: GenerateSecrets() = %v", tmpl.Name, err)
			continue
		}

		conn, err := tmpl.RenderConnection(ConnectionVars{
			Host: "svc.example", PodHost: "svc.example", Port: tmpl.Port, Password: secrets["password"],
		})
		if err != nil {
			t.Errorf("%s: RenderConnection() = %v", tmpl.Name, err)
			continue
		}
		// An unresolved placeholder means the template referenced a field that
		// does not exist, which text/template renders as "<no value>".
		if strings.Contains(conn, "<no value>") {
			t.Errorf("%s: connection has an unresolved field: %s", tmpl.Name, conn)
		}
		if !strings.Contains(conn, "svc.example") {
			t.Errorf("%s: connection does not include the host: %s", tmpl.Name, conn)
		}
	}
}

func TestGeneratedPasswordsAreURLSafe(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	tmpl, ok := c.Get("redis")
	if !ok {
		t.Fatal("redis template missing")
	}

	// Credentials are embedded in connection URLs, so any character needing
	// percent-encoding is a bug waiting for a client that forgets to escape.
	for range 200 {
		secrets, err := tmpl.GenerateSecrets()
		if err != nil {
			t.Fatal(err)
		}
		pw := secrets["password"]
		if len(pw) < 20 {
			t.Fatalf("password too short: %q", pw)
		}
		if strings.ContainsAny(pw, ":/?#[]@!$&'()*+,;=% \"\\") {
			t.Fatalf("password contains a character that needs escaping: %q", pw)
		}
	}
}

func TestUnknownFieldIsRejected(t *testing.T) {
	// KnownFields(true) is what turns a typo in a contributed template into a
	// boot failure instead of a silently ignored setting.
	var tmpl Template
	if err := decodeStrict([]byte("name: x\nnot_a_field: y\n"), &tmpl); err == nil {
		t.Fatal("decoding an unknown field succeeded, want error")
	}
}

func TestSplitImageHandlesRegistryPort(t *testing.T) {
	cases := map[string][2]string{
		"redis:7.4-alpine":         {"redis", "7.4-alpine"},
		"minio/minio:RELEASE.2024": {"minio/minio", "RELEASE.2024"},
		"redis":                    {"redis", "latest"},
		"registry:5000/redis:7":    {"registry:5000/redis", "7"},
		// The port colon must not be mistaken for the tag separator, or the
		// repository would be truncated to "registry".
		"registry:5000/redis": {"registry:5000/redis", "latest"},
	}
	for image, want := range cases {
		repo, tag := splitImage(image)
		if repo != want[0] || tag != want[1] {
			t.Errorf("splitImage(%q) = (%q, %q), want (%q, %q)",
				image, repo, tag, want[0], want[1])
		}
	}
}

func TestImageForKeepsRepository(t *testing.T) {
	tmpl := Template{Image: "redis:7.4-alpine"}

	got, err := tmpl.ImageFor("7.2")
	if err != nil {
		t.Fatalf("ImageFor = %v", err)
	}
	if got != "redis:7.2" {
		t.Errorf("ImageFor(7.2) = %q, want redis:7.2", got)
	}

	// Empty falls back to the pinned default.
	if got, _ := tmpl.ImageFor(""); got != "redis:7.4-alpine" {
		t.Errorf("ImageFor(\"\") = %q, want the template default", got)
	}

	// A user must never be able to redirect the repository. Every one of these
	// would install something other than Redis if the tag were not validated.
	for _, evil := range []string{
		"7.2 --privileged",
		"latest@sha256:deadbeef",
		"../../evil:latest",
		"7.2\nFROM evil",
		"tag/with/slash",
		"tag:with:colon",
	} {
		if _, err := tmpl.ImageFor(evil); err == nil {
			t.Errorf("ImageFor(%q) succeeded, want rejection", evil)
		}
	}
}

func TestValidateVersion(t *testing.T) {
	for _, ok := range []string{"", "7", "7.2", "7.4-alpine", "RELEASE.2024-10-13T13-34-11Z", "v1_2"} {
		if err := ValidateVersion(ok); err != nil {
			t.Errorf("ValidateVersion(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{".7", "-7", "a b", "a/b", "a:b", strings.Repeat("x", 129)} {
		if err := ValidateVersion(bad); err == nil {
			t.Errorf("ValidateVersion(%q) = nil, want error", bad)
		}
	}
}

func TestWorkloadValidation(t *testing.T) {
	base := "name: x\ndisplay_name: X\nimage: i:1\nport: 1\n" +
		"connection:\n  env_var: U\n  template: \"{{.Host}}\"\n"

	var ok Template
	if err := decodeStrict([]byte(base+"workload: statefulset\n"), &ok); err != nil {
		t.Fatal(err)
	}
	if err := ok.validate(); err != nil {
		t.Errorf("statefulset rejected: %v", err)
	}

	var bad Template
	_ = decodeStrict([]byte(base+"workload: daemonset\n"), &bad)
	if err := bad.validate(); err == nil {
		t.Error("unknown workload accepted, want error")
	}
}

func TestRequiresMustBeOneLevel(t *testing.T) {
	// A dependency that itself declares requirements is rejected at load, so the
	// provisioning graph can never be deeper than two workloads.
	c, err := Load()
	if err != nil {
		t.Fatalf("shipped catalogue does not load: %v", err)
	}
	for _, tmpl := range c.All() {
		for _, req := range tmpl.Requires {
			dep, ok := c.Get(req.Template)
			if !ok {
				t.Errorf("%s requires missing %q", tmpl.Name, req.Template)
			}
			if len(dep.Requires) > 0 {
				t.Errorf("%s depends on %s, which itself has requirements", tmpl.Name, dep.Name)
			}
		}
	}
}

func TestInjectValue(t *testing.T) {
	vars := ConnectionVars{Host: "h", Port: 5432, Password: "p"}
	cases := map[string]string{
		"host": "h", "port": "5432", "password": "p", "connection": "CONN",
	}
	for from, want := range cases {
		got := Inject{From: from}.InjectValue(vars, "CONN")
		if got != want {
			t.Errorf("from=%s: got %q, want %q", from, got, want)
		}
	}
}
