package android

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"io"
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedAgentReportsTheDriversVersion(t *testing.T) {
	zr, err := zip.NewReader(bytes.NewReader(agentAPK), int64(len(agentAPK)))
	if err != nil {
		t.Fatalf("embedded APK: %v", err)
	}
	versionLike := regexp.MustCompile(`^0\.\d+\.\d+$`)
	var versions []string
	dexes := 0
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "classes") || !strings.HasSuffix(f.Name, ".dex") {
			continue
		}
		dexes++
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		dex, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range dexStrings(t, dex) {
			if versionLike.MatchString(s) || strings.HasPrefix(s, "starting v") {
				versions = append(versions, s)
			}
		}
	}
	if dexes == 0 {
		t.Fatal("no classes*.dex in the embedded APK")
	}
	want := agentVersion()
	found := false
	for _, v := range versions {
		if v == want {
			found = true
		}
		if strings.HasPrefix(v, "starting v") && v != "starting v"+want {
			t.Errorf("the agent logs %q, the driver expects %s", v, want)
		}
	}
	if !found {
		t.Errorf("the embedded agent does not hold version %q (version-like strings in its dex: %q): bump AGENT_VERSION in Main.kt and rebuild the APK", want, versions)
	}
}

func dexStrings(t *testing.T, dex []byte) []string {
	t.Helper()
	if len(dex) < 0x70 || !bytes.HasPrefix(dex, []byte("dex\n")) {
		t.Fatalf("not a dex file")
	}
	n := binary.LittleEndian.Uint32(dex[0x38:])
	off := binary.LittleEndian.Uint32(dex[0x3c:])
	out := make([]string, 0, n)
	for i := uint32(0); i < n; i++ {
		at := int(off + 4*i)
		if at+4 > len(dex) {
			t.Fatalf("string id %d past the end", i)
		}
		p := int(binary.LittleEndian.Uint32(dex[at:]))
		for p < len(dex) && dex[p]&0x80 != 0 {
			p++
		}
		p++
		end := bytes.IndexByte(dex[p:], 0)
		if p > len(dex) || end < 0 {
			t.Fatalf("string %d unterminated", i)
		}
		out = append(out, string(dex[p:p+end]))
	}
	return out
}
