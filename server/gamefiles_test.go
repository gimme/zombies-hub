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

// Of a Black Ops III install, a player's game needs Zombies' files and what every mode has, in packs: a map's files in
// the map's own, and the rest in the one that every map needs. The other modes' files, and what BOIII and a player
// left in the folder, are not sent.
func TestT7Sends(t *testing.T) {
	for rel, pack := range map[string]string{
		"BlackOps3.exe": "", "steam_api64.dll": "", "d3dcompiler_46.dll": "", "localization.txt": "", "codlogo.bmp": "",
		"zone/base.xpak": "", "zone/base_dlc2.xpak": "", "zone/base_patch_tu32.xpak": "", "zone/initial.xpak": "",
		"zone/en_base.xpak": "", "zone/core_common.ff": "", "zone/core_common.fd": "", "zone/en_core_frontend.ff": "",
		"zone/zm_common.ff": "", "zone/zm_common_all.ff": "", "zone/zm_levelcommon.xpak": "", "zone/zm_patch.ff": "",
		"zone/zm_initial.xpak": "", "zone/en_zm_common.fd": "", "zone/ge_zm_patch.ff": "",
		"zone/snd/all/core_ui.all.sabs": "", "zone/snd/en/zm_common.en.p.sabl": "", "zone/snd/all/zm_patch.all.sabs": "",
		"video/bo3_global_logo_logosequence.mkv": "", "video/zm_frontend_load.mkv": "", "video/zm_outro_tomb.mkv": "",
		"zone/zm_factory.ff": "zm_factory", "zone/zm_factory.fd": "zm_factory", "zone/zm_factory.xpak": "zm_factory",
		"zone/zm_factory_d.xpak": "zm_factory", "zone/zm_factory_patch.ff": "zm_factory",
		"zone/en_zm_factory.ff": "zm_factory", "zone/fr_zm_factory_patch.ff": "zm_factory",
		"zone/snd/all/zm_factory.all.sabs": "zm_factory", "zone/snd/en/zm_factory_patch.en.sabl": "zm_factory",
		"zone/snd/all/zm_tomb.all.p.sabs": "zm_tomb", "video/zm_tomb_loadingmovie.mkv": "zm_tomb",
		"video/zm_factory_load_factoryloadingmovie.mkv": "zm_factory", "Zone/ZM_Zod.ff": "zm_zod",
	} {
		if got, sends := t7Sends(rel); !sends || got != pack {
			t.Errorf("%s: sent %v, in pack %q, want pack %q", rel, sends, got, pack)
		}
	}
	for _, rel := range []string{
		"boiii.exe", "ext.dll", "CrashUploader.exe", "controller.vdf", "installscript_311210.vdf", "t7.log",
		"boiii_players/properties.json", "boiii_players/user/config.cfg", "players/config.ini",
		"zone/mp_common.ff", "zone/mp_nuketown_x.xpak", "zone/en_mp_common.ff", "zone/cp_common.xpak",
		"zone/cp_mi_cairo_aquifer.ff", "zone/en_cp_common.ff", "zone/zm_factory.ff.part", "zone/readme.txt",
		"zone/snd/all/mp_common.all.sabs", "zone/snd/en/cp_common.en.sabl", "zone/snd/zm_common.all.sabs",
		"video/cp_ramses1_fs.mkv", "video/mp_intro.mkv", "video/zm_factory.txt",
		"usermaps/zm_test/zone/zm_test.ff", "mods/zm_mod/zone/mod.ff", "UnrankedServer/zone/core_common.ff",
	} {
		if pack, sends := t7Sends(rel); sends {
			t.Errorf("%s is sent, in pack %q", rel, pack)
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
