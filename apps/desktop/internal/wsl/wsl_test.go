package wsl

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unicode/utf16"
)

func TestDrivePathsMapThroughTheMountRoot(t *testing.T) {
	home := Home{Distribution: "Ubuntu", Share: `\\wsl$\Ubuntu\home\ada`, MountRoot: "/mnt"}
	for _, tc := range []struct {
		root, path, want string
		ok               bool
	}{
		{"/mnt", `C:\Users\Ada Lovelace\.astrlink\bin\astrlink.exe`, "/mnt/c/Users/Ada Lovelace/.astrlink/bin/astrlink.exe", true},
		{"/mnt", `\\?\D:\AstrLink\data\`, "/mnt/d/AstrLink/data", true},
		{"/mnt", `C:\`, "/mnt/c", true},
		{"", `C:\Users\ada`, "/c/Users/ada", true},
		{"/mnt", `\\server\share\x`, "", false},
		{"/mnt", `C:relative`, "", false},
		{"/mnt", "/home/ada", "", false},
	} {
		home.MountRoot = tc.root
		got, ok := home.LinuxPath(tc.path)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%q under %q: %q %v, want %q %v", tc.path, tc.root, got, ok, tc.want, tc.ok)
		}
	}
}

func TestShareLocationsNeedAWSLShareAndNameAHomeOnlyInsideOne(t *testing.T) {
	for _, tc := range []struct {
		path, distribution, home string
		ok                       bool
	}{
		{`\\server\Ubuntu\home\a`, "", "", false},
		{`C:\Users\a\.claude`, "", "", false},
		{`\\wsl$\Ubuntu\home\..\etc`, "", "", false},
		{`\\wsl$\Ubuntu\etc\claude`, "Ubuntu", "", true},
		{"//wsl.localhost/Ubuntu/home/a/.codex", "Ubuntu", `\\wsl.localhost\Ubuntu\home\a`, true},
		{`\\wsl.localhost\Ubuntu-26.04\home\yayoi\.claude`, "Ubuntu-26.04", `\\wsl.localhost\Ubuntu-26.04\home\yayoi`, true},
		{`\\?\UNC\wsl$\Debian\root\.pi\agent`, "Debian", `\\wsl$\Debian\root`, true},
	} {
		distribution, home, ok := ShareLocation(tc.path)
		if distribution != tc.distribution || home != tc.home || ok != tc.ok {
			t.Errorf("%q: %q %q %v, want %q %q %v", tc.path, distribution, home, ok, tc.distribution, tc.home, tc.ok)
		}
	}
}

func TestShareWritesSetModesOnThePathsTheDistributionSees(t *testing.T) {
	for _, tc := range []struct {
		path, distribution, linux string
		ok                        bool
	}{
		{`\\wsl$\Ubuntu-24.04\home\ada\.codex\config.toml`, "Ubuntu-24.04", "/home/ada/.codex/config.toml", true},
		{`\\?\UNC\wsl.localhost\Debian\root\.pi\agent`, "Debian", "/root/.pi/agent", true},
		{`C:\Users\ada\.codex`, "", "", false},
		{`\\wsl$\Ubuntu\home\..\etc\passwd`, "", "", false},
		{`\\wsl$\Ubuntu\home\ada lovelace\.a.tmp-7-0`, "Ubuntu", "/home/ada lovelace/.a.tmp-7-0", true},
	} {
		distribution, linux, ok := insideShare(tc.path)
		if distribution != tc.distribution || linux != tc.linux || ok != tc.ok {
			t.Errorf("%q: %q %q %v", tc.path, distribution, linux, ok)
		}
	}
	for output, want := range map[string]string{"644\n": "644", " 4755\r\n": "4755", "0": "0"} {
		if got, ok := fileMode(output); !ok || got != want {
			t.Errorf("%q: %q %v", output, got, ok)
		}
	}
	for _, output := range []string{"", "64x", "12345", "-rw-r--r--"} {
		if got, ok := fileMode(output); ok {
			t.Errorf("%q: %q", output, got)
		}
	}
}

func TestMountRootComesFromWSLConf(t *testing.T) {
	for conf, want := range map[string]string{
		"": "/mnt",
		"[boot]\nsystemd=true\n[automount]\nenabled = true\nroot = /windir/ # drives\n": "/windir",
		"[automount]\nroot = \"/\"\n":  "",
		"[network]\nroot = /x/\n":      "/mnt",
		"[Automount]\nRoot=relative\n": "/mnt",
		"[automount]\r\nroot = /w\r\n": "/w",
	} {
		if got := parseMountRoot(conf); got != want {
			t.Errorf("%q: %q, want %q", conf, got, want)
		}
	}
}

func TestMirroredNetworkingSharesLocalhost(t *testing.T) {
	host := t.TempDir()
	if LocalhostShared(host) {
		t.Fatal("shared without a .wslconfig")
	}
	for conf, shared := range map[string]bool{
		"[wsl2]\nmemory=8GB\nnetworkingMode=mirrored\n":   true,
		"[experimental]\nnetworkingMode = \"Mirrored\"\n": true,
		"[wsl2]\nnetworkingMode=nat\n":                    false,
		"[boot]\nnetworkingMode=mirrored\n":               false,
		"[wsl2]\n# networkingMode=mirrored\n":             false,
	} {
		if err := os.WriteFile(filepath.Join(host, ".wslconfig"), []byte(conf), 0o600); err != nil {
			t.Fatal(err)
		}
		if LocalhostShared(host) != shared {
			t.Errorf("%q: want %v", conf, shared)
		}
	}
}

func TestDecodesUTF16AndUTF8Listings(t *testing.T) {
	var utf16le []byte
	for _, unit := range utf16.Encode([]rune("\uFEFFUbuntu\r\nDebian\r\n")) {
		utf16le = append(utf16le, byte(unit), byte(unit>>8))
	}
	if got := decode(utf16le); got != "Ubuntu\r\nDebian\r\n" {
		t.Errorf("utf-16: %q", got)
	}
	if got := decode([]byte("Ubuntu-22.04\n")); got != "Ubuntu-22.04\n" {
		t.Errorf("utf-8: %q", got)
	}
}

func TestListingSkipsDockerAndUnusableNames(t *testing.T) {
	got := parseNames("Ubuntu\r\ndocker-desktop\r\ndocker-desktop-data\r\n\r\nArch\\x\r\nDebian\r\n")
	if !slices.Equal(got, []string{"Ubuntu", "Debian"}) {
		t.Fatalf("%q", got)
	}
}

func TestSharePathReachesTheHomeThroughTheWSLShare(t *testing.T) {
	for _, tc := range []struct{ home, want string }{
		{"/home/ada\n", `\\wsl$\Ubuntu\home\ada`},
		{"/root/", `\\wsl$\Ubuntu\root`},
		{"relative", ""},
		{"/home/../etc", ""},
	} {
		if got, ok := sharePath("Ubuntu", tc.home); got != tc.want || ok != (tc.want != "") {
			t.Errorf("%q: %q %v", tc.home, got, ok)
		}
	}
}

func TestReachSelectsDistributions(t *testing.T) {
	all := []Distribution{{"Ubuntu", true}, {"Debian", false}, {"Arch", false}}
	read := func(name, share string) (Home, error) {
		if name == "Arch" {
			return Home{}, errors.New("unable to start Arch")
		}
		return Home{Distribution: name, Share: share}, nil
	}
	configured := func(name string) string {
		if name == "Debian" {
			return `\\wsl$\Debian\root`
		}
		return ""
	}
	names := func(homes []Home) (out []string) {
		for _, h := range homes {
			out = append(out, h.Distribution)
		}
		return out
	}
	for _, tc := range []struct {
		reach              Reach
		reached, unchecked []string
		fails              bool
	}{
		{Running(), []string{"Ubuntu"}, []string{"Debian", "Arch"}, false},
		{RunningAnd([]string{"debian"}), []string{"Ubuntu", "Debian"}, []string{"Arch"}, false},
		{Named([]string{"Debian"}), []string{"Debian"}, []string{"Ubuntu", "Arch"}, false},
		{RunningAmong([]string{"Ubuntu", "Debian"}), []string{"Ubuntu"}, []string{"Debian", "Arch"}, false},
		{Named([]string{"Arch"}), nil, nil, true},
		{RunningAnd(nil), []string{"Ubuntu"}, []string{"Debian", "Arch"}, false},
	} {
		found, unchecked, err := homes(all, configured, tc.reach, read)
		if (err != nil) != tc.fails || !slices.Equal(names(found), tc.reached) || !slices.Equal(unchecked, tc.unchecked) {
			t.Errorf("%+v: %v %v %v", tc.reach, names(found), unchecked, err)
		}
	}
	// A distribution that fails to answer while not named is left unchecked.
	all[2].Running = true
	if found, unchecked, err := homes(all, configured, Running(), read); err != nil || len(found) != 1 || !slices.Equal(unchecked, []string{"Debian", "Arch"}) {
		t.Errorf("%v %v %v", found, unchecked, err)
	}
	if found, _, _ := Homes(configured, Named(nil)); found != nil {
		t.Errorf("named none: %v", found)
	}
}
