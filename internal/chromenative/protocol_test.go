package chromenative

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	payload, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestParseRequestFixtures(t *testing.T) {
	request, err := ParseRequest(fixture(t, "hello.json"))
	if err != nil {
		t.Fatal(err)
	}
	if request.ID != "hello-1" || request.Type != "hello" {
		t.Fatalf("unexpected request: %#v", request)
	}
	if _, err := ParseRequest(fixture(t, "unknown-field.json")); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field should fail closed, got %v", err)
	}
}

func TestNativeFrameRoundTrip(t *testing.T) {
	want := Response{Protocol: ProtocolVersion, ID: "test-1", OK: true, Result: map[string]any{"ok": true}}
	var buffer bytes.Buffer
	if err := WriteFrame(&buffer, want); err != nil {
		t.Fatal(err)
	}
	payload, err := ReadFrame(&buffer)
	if err != nil {
		t.Fatal(err)
	}
	var got Response
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.Protocol != want.Protocol || got.ID != want.ID || !got.OK {
		t.Fatalf("round trip mismatch: %#v", got)
	}
}

func TestNativeFrameRejectsOversizeBeforeAllocation(t *testing.T) {
	var frame bytes.Buffer
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], MaxMessageSize+1)
	frame.Write(header[:])
	if _, err := ReadFrame(&frame); err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("oversized frame should fail, got %v", err)
	}
}

func TestBrowserContextFixtureAndSchemes(t *testing.T) {
	request, err := ParseRequest(fixture(t, "browser-context.json"))
	if err != nil {
		t.Fatal(err)
	}
	context, err := ParseBrowserContext(request.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if context.URL != "https://example.com/docs" || context.Selection != "selected text" {
		t.Fatalf("unexpected browser context: %#v", context)
	}
	for _, invalid := range []string{"file:///C:/secret", "chrome://settings", "javascript:alert(1)"} {
		payload, _ := json.Marshal(BrowserContext{URL: invalid})
		if _, err := ParseBrowserContext(payload); err == nil {
			t.Fatalf("URL %q should be rejected", invalid)
		}
	}
}

func TestValidateOriginRequiresExactPublishedID(t *testing.T) {
	id := "abcdefghijklmnopabcdefghijklmnop"
	if err := ValidateOrigin("chrome-extension://"+id+"/", id); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		"chrome-extension://" + id,
		"chrome-extension://ponmlkjihgfedcbaponmlkjihgfedcba/",
		"https://example.com/",
	} {
		if err := ValidateOrigin(invalid, id); err == nil {
			t.Fatalf("origin %q should be rejected", invalid)
		}
	}
}
