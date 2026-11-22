package launcherapi

import "testing"

func TestLocalPath(t *testing.T) {
	for path, want := range map[string]bool{
		"binkw32.dll":                    true,
		"zone/all/zm_transit.ff":         true,
		"video/zm (1).webm":              true,
		"redist/DirectX/Jun2010_x64.cab": true,
		"$recycle/a":                     true,
		"":                               false,
		"/binkw32.dll":                   false,
		"zone//a.ff":                     false,
		"zone/../a.ff":                   false,
		"../a.ff":                        false,
		"zone/.":                         false,
		".hidden":                        false,
		"zone\\all\\a.ff":                false,
		"C:/a.ff":                        false,
		"zone/a.ff ":                     false,
		"zone/a.":                        false,
		"zone /a.ff":                     false,
		"zone/con":                       false,
		"zone/NUL.ff":                    false,
		"zone/com1.txt":                  false,
		"zone/console.ff":                true,
		"zone/a;b.ff":                    false,
		"zone/a%20b.ff":                  false,
		"zone/a\"b.ff":                   false,
		"zone/é.ff":                      false,
		"a:stream":                       false,
	} {
		if got := LocalPath(path); got != want {
			t.Errorf("%q: %v", path, got)
		}
	}
	long := ""
	for len(long) <= 200 {
		long += "abcdefghi/"
	}
	if LocalPath(long + "a") {
		t.Error("a path of more than 200 characters is local")
	}
}

func TestHashOf(t *testing.T) {
	h := NewHash()
	h.Write([]byte("abc"))
	if got := HashOf(h); got != "ba7816bf8f01cfea414140de5dae2223" {
		t.Errorf("the hash of abc: %s", got)
	}
}
