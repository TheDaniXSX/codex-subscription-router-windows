package main

import "testing"

func TestComputerUseParentIsExactBundledPath(t *testing.T) {
	exe := `C:\Router\resources\codex.exe`
	for _, tc := range []struct {
		path string
		want bool
	}{
		{`C:\Router\resources\cua_node\bin\node_modules\@oai\sky\bin\windows\codex-computer-use.exe`, true},
		{`c:\router\resources\cua_node\bin\node_modules\@oai\sky\bin\windows\CODEX-COMPUTER-USE.EXE`, true},
		{`C:\Other\codex-computer-use.exe`, false},
		{`C:\Router\ChatGPT.real.exe`, false},
		{`C:\Router\resources\codex.real.exe`, false},
	} {
		if got := matchesComputerUseParent(exe, tc.path); got != tc.want {
			t.Fatalf("%s: got %v", tc.path, got)
		}
	}
	if isComputerUseAuxiliary([]string{"app-server", "--analytics-default-enabled"}) {
		t.Fatal("desktop cannot bypass mux")
	}
	if isComputerUseAuxiliary([]string{"exec", "test"}) {
		t.Fatal("inference command cannot use auxiliary exception")
	}
}
