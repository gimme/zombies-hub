package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Of a Black Ops II install, the server takes Zombies' files and what every mode has, and not the other modes'.
func TestT6Takes(t *testing.T) {
	for _, rel := range []string{
		"t6zm.exe", "binkw32.dll", "steam_api.dll", "localization.txt", "localization_zm.txt", "codlogo.bmp",
		"installscript.vdf", "redist/DirectX/DXSETUP.exe", "Redist/vcredist_x86.exe",
		"zone/all/base.ipak", "zone/all/zm.ipak", "zone/all/dlczm1.ipak", "zone/all/dlczm0_load_zm.ff",
		"zone/all/patch_zm.ff", "zone/all/patch_zm.ipak", "zone/all/common_zm.ff", "zone/all/code_post_gfx_zm.ff",
		"zone/all/ui_zm.ff", "zone/all/dlc1_load_zm.ff", "zone/all/so_zclassic_zm_buried.ff", "zone/all/weapons.ff",
		"zone/all/weapons.clump", "zone/all/zm_transit.ff", "zone/all/zm_transit_gump_town.ff",
		"zone/all/zm_nuked_patch.ipak", "zone/english/en_base.ipak", "zone/english/en_zm_tomb.ff",
		"zone/english/en_patch_ui_zm.ff", "zone/german/ge_common_zm.ff",
		"sound/zmb_tomb.all.sabs", "sound/zmb_common.english.sabl", "sound/cmn_root.all.sabs",
		"sound/wpn_slingshot.all.sabl", "video/zm_tomb_load.webm", "video/default.bik", "video/treyarch.webm",
	} {
		if !t6Takes(rel) {
			t.Errorf("%s is not taken", rel)
		}
	}
	for _, rel := range []string{
		"t6mp.exe", "t6sp.exe", "localization_mp.txt", "localization_sp.txt", "players/config.cfg",
		"zone/all/mp_nuketown_2020.ff", "zone/all/common_mp.ff", "zone/all/patch_mp.ff", "zone/all/mp.ipak",
		"zone/all/dlc1.ipak", "zone/all/sp.ipak", "zone/all/angola.ff", "zone/all/common.ff", "zone/all/patch.ff",
		"zone/english/en_common_mp.ff", "zone/english/en_la_1.ff", "sound/mpl_nuketown.all.sabs",
		"sound/angola.all.sabs", "video/mp_nuketown_load.webm", "video/angola_load.webm", "mods/zm_mine/mod.ff",
	} {
		if t6Takes(rel) {
			t.Errorf("%s is taken", rel)
		}
	}
}

// Of the folder of Steam's dedicated server of Black Ops III, the server takes the server without its multiplayer maps.
func TestT7ServerTakes(t *testing.T) {
	for rel, want := range map[string]bool{
		"UnrankedServer/BlackOps3_UnrankedDedicatedServer.exe":                true,
		"UnrankedServer/zone/core_common.ff":                                  true,
		"UnrankedServer/zone/zm_factory.ff":                                   true,
		"UnrankedServer/identities/dedicatedpc/LPC/core_sv_ffotd_tu32_593.ff": true,
		"UnrankedServer/zone/mp_nuketown_x.ff":                                false,
		"UnrankedServer/zone/en_mp_common.ff":                                 false,
		"BlackOps3.exe":                                                       false,
		"zone/zm_factory.ff":                                                  false,
	} {
		if got := t7ServerTakes(rel); got != want {
			t.Errorf("%s: taken %v, want %v", rel, got, want)
		}
	}
}

// zombies-server game-files prints what the server takes of a folder, for a script to copy.
func TestGameFilesCommand(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{"zone/zm_factory.ff", "zone/zm_factory.xpak", "zone/mp_nuketown_x.ff",
		"zone/snd/zm_factory.sabs", "BlackOps3.exe"} {
		writeTestFile(t, filepath.Join(dir, filepath.FromSlash(rel)))
	}
	stdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	code := gameFilesCommand([]string{t7, dir})
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)
	if want := []byte("./zone/zm_factory.ff\x00"); code != 0 || !bytes.Equal(out, want) {
		t.Errorf("game-files t7: exit %d, %q, want %q", code, out, want)
	}
	if code := gameFilesCommand([]string{"t9", dir}); code != 2 {
		t.Errorf("game-files of a folder the game files don't have: exit %d, want 2", code)
	}
	if !strings.HasPrefix(t7Exe, "UnrankedServer/") || !t7ServerTakes(t7Exe) {
		t.Errorf("the server's program, %s, is not taken", t7Exe)
	}
}
