package instance

import (
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestBothInstancesListTheSameDirectories(t *testing.T) {
	support := filepath.FromSlash("/home/a/.local/share")
	every := []string{filepath.Join(support, Identifier), filepath.Join(support, IsolatedIdentifier)}
	for _, own := range every {
		if got := EveryInstanceDirectory(own); !reflect.DeepEqual(got, every) {
			t.Fatalf("EveryInstanceDirectory(%q) = %q", own, got)
		}
	}
	other := filepath.Join(support, "com.astrlink.desktop.dev")
	if got := EveryInstanceDirectory(other); !reflect.DeepEqual(got, []string{other}) {
		t.Fatalf("other = %q", got)
	}
	root := filepath.FromSlash("/")
	if got := EveryInstanceDirectory(root); !reflect.DeepEqual(got, []string{root}) {
		t.Fatalf("root = %q", got)
	}
}

func TestTheIsolatedInstanceKeepsTheTauriDevelopmentIdentity(t *testing.T) {
	for _, c := range []struct {
		instance                        Instance
		identifier, name, scheme, group string
		port                            uint16
		script                          string
	}{
		{Current(false), "com.astrlink.desktop", "AstrLink", "astrlink", "7XWQYGN832.com.astrlink.desktop", 18317, "window.__ASTRLINK_ISOLATED__ = false;"},
		{Current(true), "com.astrlink.desktop.isolated", "AstrLink Dev", "astrlink-isolated", "7XWQYGN832.com.astrlink.desktop.isolated", 18318, "window.__ASTRLINK_ISOLATED__ = true;"},
	} {
		i := c.instance
		if i.Identifier() != c.identifier || i.Name() != c.name || i.LinkScheme() != c.scheme ||
			i.AppGroup() != c.group || i.DefaultInferencePort() != c.port || i.InitializationScript() != c.script {
			t.Errorf("%+v: %s %s %s %s %d %s", i, i.Identifier(), i.Name(), i.LinkScheme(), i.AppGroup(), i.DefaultInferencePort(), i.InitializationScript())
		}
	}
}

func TestDirectoriesFollowTauriNaming(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(name string) string { return vars[name] }
	}
	// abs makes a slash path absolute on this host, where the XDG variables
	// are checked.
	abs := func(p string) string {
		if runtime.GOOS == "windows" {
			return "C:" + p
		}
		return p
	}
	for _, c := range []struct {
		goos, home string
		vars       map[string]string
		want       Directories
	}{
		{"darwin", "/Users/a", nil, Directories{
			Data:   "/Users/a/Library/Application Support/com.astrlink.desktop",
			Config: "/Users/a/Library/Application Support/com.astrlink.desktop",
		}},
		{"linux", "/home/a", nil, Directories{
			Data:   "/home/a/.local/share/com.astrlink.desktop",
			Config: "/home/a/.config/com.astrlink.desktop",
		}},
		{"linux", "/home/a", map[string]string{"XDG_DATA_HOME": abs("/d"), "XDG_CONFIG_HOME": abs("/c")}, Directories{
			Data: abs("/d/com.astrlink.desktop"), Config: abs("/c/com.astrlink.desktop"),
		}},
		// XDG variables that are not absolute paths are ignored.
		{"linux", "/home/a", map[string]string{"XDG_DATA_HOME": "rel"}, Directories{
			Data: "/home/a/.local/share/com.astrlink.desktop", Config: "/home/a/.config/com.astrlink.desktop",
		}},
	} {
		got, err := dirsFor(c.goos, c.home, env(c.vars), Identifier)
		want := Directories{Data: filepath.FromSlash(c.want.Data), Config: filepath.FromSlash(c.want.Config)}
		if err != nil || got != want {
			t.Errorf("%s %v: %+v, %v", c.goos, c.vars, got, err)
		}
	}
	got, err := dirsFor("windows", `C:\Users\a`, env(map[string]string{"APPDATA": `C:\Users\a\AppData\Roaming`}), IsolatedIdentifier)
	if want := filepath.Join(`C:\Users\a\AppData\Roaming`, IsolatedIdentifier); err != nil || got.Data != want || got.Config != want {
		t.Errorf("windows: %+v, %v", got, err)
	}
	if _, err := dirsFor("windows", `C:\Users\a`, env(nil), Identifier); err == nil {
		t.Error("windows without APPDATA")
	}
}

// Only the process `mygo dev` starts is the isolated instance; a debug build
// run on its own is the installed one.
func TestOnlyMygoDevRunsTheIsolatedInstance(t *testing.T) {
	for _, c := range []struct {
		env      map[string]string
		isolated bool
	}{
		{map[string]string{"MYGO_ENV": "development"}, true},
		{map[string]string{}, false},
		{map[string]string{"MYGO_ENV": ""}, false},
		{map[string]string{"MYGO_ENV": "production"}, false},
		{map[string]string{"MYGO_GENERATE": "src/mygo.ts"}, false},
		{map[string]string{"MYGO_ENV": "development", "MYGO_GENERATE": "src/mygo.ts"}, true},
	} {
		getenv := func(name string) string { return c.env[name] }
		if got := fromEnvironment(getenv); got.Isolated != c.isolated {
			t.Errorf("%v: Isolated = %v, want %v", c.env, got.Isolated, c.isolated)
		}
	}
}

func TestFromEnvironmentReadsMygoEnv(t *testing.T) {
	t.Setenv("MYGO_ENV", "development")
	if !FromEnvironment().Isolated {
		t.Fatal("MYGO_ENV=development: want the isolated instance")
	}
	t.Setenv("MYGO_ENV", "production")
	if FromEnvironment().Isolated {
		t.Fatal("MYGO_ENV=production: want the installed instance")
	}
}
