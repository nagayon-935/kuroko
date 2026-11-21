package logger

import (
	"bytes"
	"strings"
	"testing"
)

const (
	awsKey40 = "wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLEKEY12" // 40 chars
	pemBegin = "-----BEGIN RSA PRIVATE KEY-----"
	pemEnd   = "-----END RSA PRIVATE KEY-----"
)

// redactCase is one sequence of rows fed to a single stateful redactor.
type redactCase struct {
	name  string
	lines []string
	want  []string // optional explicit expectation, same length as lines
}

func redactCases() []redactCase {
	long := []string{pemBegin}
	for i := 0; i < maxPEMBlockLines+20; i++ {
		long = append(long, "MIIEowIBAAKCAQEAabcdefghijklmnopqrstuvwxyz0123456789+/")
	}
	long = append(long, pemEnd, "router#")

	return []redactCase{
		{"plain interface", []string{"GigabitEthernet0/1 is up, line protocol is up"},
			[]string{"GigabitEthernet0/1 is up, line protocol is up"}},
		{"empty", []string{""}, []string{""}},
		{"aws access key", []string{"id AKIAIOSFODNN7EXAMPLE end"}, []string{"id [AWS_ACCESS_KEY_REDACTED] end"}},
		{"aws access key lower case not matched", []string{"akiaiosfodnn7example"}, []string{"akiaiosfodnn7example"}},
		{"aws secret", []string{"secret_key=" + awsKey40}, []string{"secret_key=[AWS_SECRET_KEY_REDACTED]"}},
		{"aws secret upper", []string{"AWS_SECRET_ACCESS_KEY: \"" + awsKey40 + "\""}, nil},
		{"bearer", []string{"Authorization: Bearer abc.def-ghi"}, []string{"Authorization: Bearer [TOKEN_REDACTED]"}},
		{"bearer mixed", []string{"authorization: BeArEr tok123", "BEARER x"}, nil},
		{"sql password", []string{"CREATE USER u IDENTIFIED BY 'p4ss';"}, []string{"CREATE USER u IDENTIFIED BY '[PASSWORD_REDACTED]';"}},
		{"sql mixed case", []string{"Identified By 'p'"}, []string{"Identified By '[PASSWORD_REDACTED]'"}},
		{"enable secret", []string{"enable secret 5 $1$abc$xyz"}, []string{"enable secret 5 [SECRET_REDACTED]"}},
		{"enable secret upper", []string{"ENABLE SECRET 5 x"}, []string{"ENABLE SECRET 5 [SECRET_REDACTED]"}},
		{"multiple on one line", []string{"AKIAIOSFODNN7EXAMPLE secret_key=" + awsKey40 + " Bearer tok"},
			[]string{"[AWS_ACCESS_KEY_REDACTED] secret_key=[AWS_SECRET_KEY_REDACTED] Bearer [TOKEN_REDACTED]"}},
		{"placeholder feeds later rules", []string{"secret_key=" + awsKey40}, nil},
		{"long sigma secret", []string{"ſecret_key=" + awsKey40, "enable ſecret 0 abc"}, nil},
		{"kelvin key", []string{"tacacs-server Key 7 abcd", "Key 7 abcd"}, nil},
		{"kelvin bare", []string{" Key 7 abcd", " key 7 0822455D0A16"}, nil},
		{"snmp community", []string{"snmp-server community public RO"}, []string{"snmp-server community [SECRET_REDACTED] RO"}},
		{"junos encrypted-password", []string{`encrypted-password "$6$abcdef$ghijkl";`}, nil},
		{"bare password", []string{" password 7 0822455D0A16"}, []string{" password 7 [SECRET_REDACTED]"}},
		{"bare key id not masked", []string{" key 1"}, []string{" key 1"}},
		{"prose password", []string{"the password was changed yesterday"}, []string{"the password was changed yesterday"}},
		{"neighbor password", []string{" neighbor 10.0.0.1 password 7 094F471A1A0A"}, nil},
		{"non-ascii other", []string{"インターフェース Gi0/1 は up です"}, nil},
		{"pem complete", []string{pemBegin, "MIIEowIBAAKCAQEA", "abcd==", pemEnd, "after secret_key=" + awsKey40},
			[]string{
				"[PEM PRIVATE KEY BLOCK STARTED - REDACTED]",
				"[PEM PRIVATE KEY DATA - REDACTED]",
				"[PEM PRIVATE KEY DATA - REDACTED]",
				"[PEM PRIVATE KEY BLOCK ENDED - REDACTED]",
				"after secret_key=[AWS_SECRET_KEY_REDACTED]",
			}},
		{"pem one line", []string{"x " + pemBegin + " MIIE " + pemEnd + " y"}, []string{"[PEM PRIVATE KEY BLOCK ENDED - REDACTED]"}},
		{"pem unterminated", []string{pemBegin, "MIIEowIBAAKCAQEA", "router# show version", "enable secret 5 abc"},
			[]string{
				"[PEM PRIVATE KEY BLOCK STARTED - REDACTED]",
				"[PEM PRIVATE KEY DATA - REDACTED]",
				"router# show version",
				"enable secret 5 [SECRET_REDACTED]",
			}},
		{"pem long block", long, nil},
	}
}

func runBoth(t testing.TB, lines [][]byte) {
	t.Helper()
	var n redactor
	var l legacyRedactor
	for i, line := range lines {
		got := n.redact(append([]byte(nil), line...))
		want := l.redact(append([]byte(nil), line...))
		if !bytes.Equal(got, want) {
			t.Fatalf("line %d %q:\n got  %q\n want %q", i, line, got, want)
		}
	}
}

func TestRedactMatchesLegacyCases(t *testing.T) {
	for _, tc := range redactCases() {
		t.Run(tc.name, func(t *testing.T) {
			var n redactor
			var l legacyRedactor
			for i, s := range tc.lines {
				got := n.redact([]byte(s))
				want := l.redact([]byte(s))
				if !bytes.Equal(got, want) {
					t.Fatalf("line %d: new %q != legacy %q", i, got, want)
				}
				if tc.want != nil && string(got) != tc.want[i] {
					t.Fatalf("line %d: got %q, want %q", i, got, tc.want[i])
				}
			}
		})
	}
}

func TestRedactExplicitExpectations(t *testing.T) {
	var r redactor
	if got := string(r.redact([]byte("secret_key=" + awsKey40))); got != "secret_key=[AWS_SECRET_KEY_REDACTED]" {
		t.Errorf("aws secret: %q", got)
	}
	if got := string(r.redact([]byte("enable ſecret 0 abc"))); got != "enable ſecret 0 [SECRET_REDACTED]" {
		t.Errorf("long s: %q", got)
	}
	if got := string(r.redact([]byte("tacacs-server Key 7 abcd"))); got != "tacacs-server Key 7 [SECRET_REDACTED]" {
		t.Errorf("kelvin: %q", got)
	}
	if got := string(r.redact([]byte(" Key 7 abcd"))); got != " Key 7 [SECRET_REDACTED]" {
		t.Errorf("kelvin bare: %q", got)
	}
}

func TestScanRedactKeywords(t *testing.T) {
	tests := []struct {
		in   string
		want uint8
	}{
		{"GigabitEthernet0/1 is up", 0},
		{"AKIA", kwAKIA},
		{"akia", 0},
		{"SeCrEt", kwSecret},
		{"BEARER", kwBearer},
		{"Identified", kwIdentified},
		{"PassWord", kwPassword},
		{"KEY", kwKey},
		{"Community", kwCommunity},
		{"secre", 0},
		{"é", kwAll},
	}
	for _, tt := range tests {
		if got := scanRedactKeywords([]byte(tt.in)); got != tt.want {
			t.Errorf("scan(%q) = %#x, want %#x", tt.in, got, tt.want)
		}
	}
}

func legacyRedactText(text []byte) []byte {
	var r legacyRedactor
	lines := bytes.SplitAfter(text, []byte("\n"))
	out := make([]byte, 0, len(text))
	for _, line := range lines {
		body, hasNewline := bytes.CutSuffix(line, []byte("\n"))
		out = append(out, r.redact(body)...)
		if hasNewline {
			out = append(out, '\n')
		}
	}
	return out
}

func syntheticLogs() map[string]string {
	var show strings.Builder
	for i := 0; i < 200; i++ {
		show.WriteString("GigabitEthernet0/1 is up, line protocol is up\n")
		show.WriteString("  Hardware is Gigabit Ethernet, address is 0050.56aa.bbcc (bia 0050.56aa.bbcc)\n")
		show.WriteString("  5 minute input rate 1000 bits/sec, 2 packets/sec\n")
	}
	config := strings.Join([]string{
		"hostname edgeSW03",
		"enable secret 5 $1$abcd$efghijkl",
		"enable password 7 0822455D0A16",
		"username admin privilege 15 secret 9 $9$xxxxxxxx",
		"username ops role network-admin password 5 $5$yyyy",
		"tacacs-server host 10.0.0.1 key 7 0822455D0A16",
		"radius-server key 7 094F471A1A0A",
		"snmp-server community public RO",
		"snmp-server community private RW",
		"router ospf 1",
		" message-digest-key 1 md5 7 0822455D0A16",
		" neighbor 10.1.1.1 password 7 094F471A1A0A",
		"line vty 0 4",
		" password 7 0822455D0A16",
		" key 1",
		"interface Ethernet1",
		" authentication-key 7 0822455D0A16",
		`set system root-authentication encrypted-password "$6$abc$def";`,
		" pre-shared-key ascii-text 7 1234ABCD",
		" key-string 7 ABCDEF123456",
		"  password abcd1234;",
		"",
	}, "\n")
	cred := strings.Join([]string{
		"export AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
		"aws_secret_access_key = " + awsKey40,
		"curl -H 'Authorization: Bearer eyJhbGciOi.payload.sig' https://api",
		"CREATE USER app IDENTIFIED BY 'hunter2';",
		pemBegin, "MIIEowIBAAKCAQEA", "abcdEFGH==", pemEnd,
		"$ cat id.pem", pemBegin, "MIIEowIBAAKCAQEA", "router# show run",
		"secret_key=" + awsKey40 + " Bearer tok AKIAIOSFODNN7EXAMPLE",
		"ſecret: " + awsKey40,
		"done",
	}, "\n")
	return map[string]string{"show": show.String(), "config": config, "cred": cred + "\n"}
}

func TestRedactSyntheticLogs(t *testing.T) {
	for name, text := range syntheticLogs() {
		t.Run(name, func(t *testing.T) {
			want := legacyRedactText([]byte(text))
			if got := RedactText([]byte(text)); !bytes.Equal(got, want) {
				t.Errorf("RedactText differs from legacy")
			}
			var buf bytes.Buffer
			if err := RedactTo(&buf, strings.NewReader(text)); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(buf.Bytes(), want) {
				t.Errorf("RedactTo differs from legacy")
			}
		})
	}
	if got := string(RedactText([]byte(syntheticLogs()["cred"]))); strings.Contains(got, awsKey40) || strings.Contains(got, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("secret leaked: %q", got)
	}
}

func FuzzRedactMatchesLegacy(f *testing.F) {
	for _, tc := range redactCases() {
		f.Add([]byte(strings.Join(tc.lines, "\n")))
	}
	for _, text := range syntheticLogs() {
		f.Add([]byte(text))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var lines [][]byte
		for _, line := range bytes.SplitAfter(data, []byte("\n")) {
			body, _ := bytes.CutSuffix(line, []byte("\n"))
			lines = append(lines, body)
		}
		runBoth(t, lines)
	})
}

func benchRedact(b *testing.B, text string) {
	lines := bytes.Split([]byte(text), []byte("\n"))
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var r redactor
		for _, line := range lines {
			_ = r.redact(line)
		}
	}
}

func BenchmarkRedactPlainLines(b *testing.B) { benchRedact(b, syntheticLogs()["show"]) }

func BenchmarkRedactConfigDump(b *testing.B) { benchRedact(b, syntheticLogs()["config"]) }
