package core

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestDownloadFilename(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"report.txt", "report.txt"},
		{".config", ".config"},
		{"Pot 通信记录 · 修订 02（A/B 局部对照）.zip", "Pot 通信记录 · 修订 02（A_B 局部对照）.zip"},
		{`A\B.html`, "A_B.html"},
		{"../escape", ".._escape"},
		{"a\x00\r\n\t\x7f\u0085b.txt", "a______b.txt"},
		{`a<>:"|?*.txt`, "a_______.txt"},
		{"", "resource"}, {"..", "resource"}, {" . ", "resource"},
		{"report.txt. ", "report.txt"},
		{"con.txt", "_con.txt"}, {"NUL.tar.gz", "_NUL.tar.gz"},
		{"AUX", "_AUX"}, {"PRN ", "_PRN"}, {"COM1.txt", "_COM1.txt"},
		{"LPT9.log", "_LPT9.log"}, {"com¹.txt", "_com¹.txt"},
		{"COM0.txt", "COM0.txt"}, {"LPT10.txt", "LPT10.txt"},
		{"CONIN$.txt", "_CONIN$.txt"},
		{strings.Repeat("中", 100) + ".html", strings.Repeat("中", 83) + ".html"},
		{strings.Repeat("😀", 100) + ".zip", strings.Repeat("😀", 62) + ".zip"},
		{"a." + strings.Repeat("x", 300), "a." + strings.Repeat("x", 253)},
		{"中." + strings.Repeat("x", 253), "r." + strings.Repeat("x", 253)},
		{strings.Repeat("a", 255), strings.Repeat("a", 255)},
	} {
		t.Run(test.input, func(t *testing.T) {
			got := DownloadFilename(test.input)
			if got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
			checkDownloadFilename(t, got)
		})
	}
}

func checkDownloadFilename(t *testing.T, got string) {
	t.Helper()
	if got == "" || len(got) > 255 || !utf8.ValidString(got) || strings.ContainsAny(got, `/\<>:"|?*`) || strings.HasSuffix(got, ".") || strings.HasSuffix(got, " ") {
		t.Fatalf("unsafe filename %q", got)
	}
	for _, r := range got {
		if unicode.IsControl(r) {
			t.Fatalf("control character in %q", got)
		}
	}
	if twice := DownloadFilename(got); got != twice {
		t.Fatalf("conversion is not stable: %q -> %q", got, twice)
	}
}

func FuzzDownloadFilename(f *testing.F) {
	for _, seed := range []string{"", "A/B", `A\B`, "CON.txt", ".", strings.Repeat("中文😀", 100) + ".zip"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) { checkDownloadFilename(t, DownloadFilename(input)) })
}
