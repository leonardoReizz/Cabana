package deploy

import (
	"testing"

	"github.com/leonardoreis/cabana/internal/domain"
)

// The missing .git suffix silently turns a git build into an HTML download that
// fails with a confusing Dockerfile parse error, so it is worth pinning down.
func TestGitContextURL(t *testing.T) {
	cases := map[string]struct {
		repo, ref, want string
	}{
		"browser URL gets .git appended": {
			"https://github.com/you/repo", "main",
			"https://github.com/you/repo.git#main",
		},
		"existing .git is not doubled": {
			"https://github.com/you/repo.git", "main",
			"https://github.com/you/repo.git#main",
		},
		"trailing slash is trimmed": {
			"https://github.com/you/repo/", "master",
			"https://github.com/you/repo.git#master",
		},
		"surrounding whitespace is trimmed": {
			"  https://github.com/you/repo  ", "main",
			"https://github.com/you/repo.git#main",
		},
		"empty ref omits the fragment": {
			"https://github.com/you/repo", "",
			"https://github.com/you/repo.git",
		},
		"tags and SHAs pass through": {
			"https://github.com/you/repo", "v1.2.3",
			"https://github.com/you/repo.git#v1.2.3",
		},
	}

	for name, tc := range cases {
		if got := gitContextURL(tc.repo, tc.ref); got != tc.want {
			t.Errorf("%s: gitContextURL(%q, %q) = %q, want %q",
				name, tc.repo, tc.ref, got, tc.want)
		}
	}
}

func TestEnvSecretPinsPort(t *testing.T) {
	secret := envSecretObject(testApp(), map[string]string{"PORT": "3000", "API_KEY": "x"})

	// A user-supplied PORT must not win: the Service targets 8080, so honouring
	// it would produce an app that builds, runs, and is unreachable.
	if got := secret.StringData["PORT"]; got != "8080" {
		t.Errorf("PORT = %q, want 8080 (cabana must override the user value)", got)
	}
	if got := secret.StringData["API_KEY"]; got != "x" {
		t.Errorf("API_KEY = %q, want x", got)
	}
}

func TestImageRefIsTaggedPerDeploy(t *testing.T) {
	d := &K3s{registry: "reg:5000"}
	app := testApp()

	first := d.imageRef(app, "deploy1")
	second := d.imageRef(app, "deploy2")

	if first == second {
		t.Fatal("image refs must differ per deploy, otherwise rollback is impossible")
	}
	if want := "reg:5000/hello:deploy1"; first != want {
		t.Errorf("imageRef = %q, want %q", first, want)
	}
}

func testApp() domain.App {
	return domain.App{ID: "app123", Name: "hello", RepoURL: "https://github.com/you/hello", GitRef: "main"}
}
