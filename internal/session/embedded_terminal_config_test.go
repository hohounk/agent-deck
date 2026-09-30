package session

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func TestEmbeddedTerminalDefaultsOff(t *testing.T) {
	if (UISettings{}).GetEmbeddedTerminal() {
		t.Fatal("unset [ui].embedded_terminal should preserve the classic layout")
	}
}

func TestEmbeddedTerminalCanBeDisabled(t *testing.T) {
	var cfg UserConfig
	if _, err := toml.Decode("[ui]\nembedded_terminal = false\n", &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.UI.GetEmbeddedTerminal() {
		t.Fatal("explicit embedded_terminal=false was ignored")
	}
}

func TestEmbeddedTerminalExplicitTrueStaysOn(t *testing.T) {
	var cfg UserConfig
	if _, err := toml.Decode("[ui]\nembedded_terminal = true\n", &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !cfg.UI.GetEmbeddedTerminal() {
		t.Fatal("explicit embedded_terminal=true was ignored")
	}
}

func TestMergePanelConfigPropagatesEmbeddedTerminalOff(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	on := true
	if err := SaveUserConfig(&UserConfig{UI: UISettings{EmbeddedTerminal: &on}}); err != nil {
		t.Fatalf("save starting config: %v", err)
	}
	off := false
	merged, err := MergePanelConfigOntoDisk(&UserConfig{UI: UISettings{EmbeddedTerminal: &off}})
	if err != nil {
		t.Fatalf("merge settings panel config: %v", err)
	}
	if merged.UI.EmbeddedTerminal == nil || *merged.UI.EmbeddedTerminal {
		t.Fatal("settings merge dropped explicit embedded_terminal=false")
	}
}

func TestSidebarDensityNormalizesAndDefaults(t *testing.T) {
	cases := map[string]string{
		"":          SidebarDensityCompact,
		"nonsense":  SidebarDensityCompact,
		"full":      SidebarDensityFull,
		" Compact ": SidebarDensityCompact,
		"MINIMAL":   SidebarDensityMinimal,
		"auto":      SidebarDensityAuto,
	}
	for raw, want := range cases {
		if got := (UISettings{SidebarDensity: raw}).GetSidebarDensity(); got != want {
			t.Fatalf("sidebar_density %q resolved to %q, want %q", raw, got, want)
		}
	}
}

func TestMergePanelConfigPropagatesSidebarDensity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	if err := SaveUserConfig(&UserConfig{UI: UISettings{SidebarDensity: SidebarDensityFull}}); err != nil {
		t.Fatalf("save starting config: %v", err)
	}
	merged, err := MergePanelConfigOntoDisk(&UserConfig{UI: UISettings{SidebarDensity: SidebarDensityAuto}})
	if err != nil {
		t.Fatalf("merge settings panel config: %v", err)
	}
	if merged.UI.SidebarDensity != SidebarDensityAuto {
		t.Fatalf("settings merge dropped sidebar_density=auto, got %q", merged.UI.SidebarDensity)
	}
	merged, err = MergePanelConfigOntoDisk(&UserConfig{})
	if err != nil {
		t.Fatalf("merge empty panel config: %v", err)
	}
	if merged.UI.SidebarDensity != SidebarDensityFull {
		t.Fatalf("empty panel density overwrote the stored value, got %q", merged.UI.SidebarDensity)
	}
}

// TestSidebarContextTokensParsesFromTOML: the count is on unless the config
// turns it off, so an unset key must read as true and only an explicit
// false hides it.
func TestSidebarContextTokensParsesFromTOML(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantNil bool
		want    bool
	}{
		{name: "unset", body: "[ui]\n", wantNil: true, want: true},
		{name: "true", body: "[ui]\nsidebar_context_tokens = true\n", want: true},
		{name: "false", body: "[ui]\nsidebar_context_tokens = false\n", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg UserConfig
			if _, err := toml.Decode(tc.body, &cfg); err != nil {
				t.Fatalf("decode config: %v", err)
			}
			if gotNil := cfg.UI.SidebarContextTokens == nil; gotNil != tc.wantNil {
				t.Fatalf("SidebarContextTokens nil = %v, want %v", gotNil, tc.wantNil)
			}
			if got := cfg.UI.GetSidebarContextTokens(); got != tc.want {
				t.Fatalf("GetSidebarContextTokens() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMergePanelConfigKeepsSidebarContextTokens: the settings panel has no
// control for the key, so a panel save must keep the value on disk.
func TestMergePanelConfigKeepsSidebarContextTokens(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	off := false
	if err := SaveUserConfig(&UserConfig{UI: UISettings{SidebarContextTokens: &off}}); err != nil {
		t.Fatalf("save starting config: %v", err)
	}
	merged, err := MergePanelConfigOntoDisk(&UserConfig{})
	if err != nil {
		t.Fatalf("merge settings panel config: %v", err)
	}
	if merged.UI.SidebarContextTokens == nil || *merged.UI.SidebarContextTokens {
		t.Fatal("settings merge dropped sidebar_context_tokens=false")
	}
}
