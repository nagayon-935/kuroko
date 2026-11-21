package logger

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestRedactText(t *testing.T) {
	in := "# Command : aws s3 ls\n" +
		"export AWS_ACCESS_KEY_ID=AKIAABCDEFGHIJKLMNOP\n" +
		"curl -H 'Authorization: Bearer abc.def-ghi'\n" +
		"-----BEGIN OPENSSH PRIVATE KEY-----\n" +
		"b3BlbnNzaC1rZXktdjEAAAAA\n" +
		"-----END OPENSSH PRIVATE KEY-----\n" +
		"plain output"
	want := "# Command : aws s3 ls\n" +
		"export AWS_ACCESS_KEY_ID=[AWS_ACCESS_KEY_REDACTED]\n" +
		"curl -H 'Authorization: Bearer [TOKEN_REDACTED]'\n" +
		"[PEM PRIVATE KEY BLOCK STARTED - REDACTED]\n" +
		"[PEM PRIVATE KEY DATA - REDACTED]\n" +
		"[PEM PRIVATE KEY BLOCK ENDED - REDACTED]\n" +
		"plain output"
	if got := string(RedactText([]byte(in))); got != want {
		t.Fatalf("RedactText =\n%s\nwant\n%s", got, want)
	}
}

func TestLoggerRawDebugSkippedWhenRedacting(t *testing.T) {
	t.Setenv("KUROKO_RAW_DEBUG", "1")
	l, err := New(t.TempDir(), []string{"bash"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write([]byte("export AWS_ACCESS_KEY_ID=AKIAABCDEFGHIJKLMNOP\n")); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(l.Path + ".raw"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("raw debug file exists with redaction enabled (stat err = %v)", err)
	}
}

func TestRedactPEMBlockWithoutEndDoesNotSwallowTheSession(t *testing.T) {
	// Arrange: `head -2` prints the BEGIN line but never the END line.
	input := strings.Join([]string{
		"user@h:~$ head -2 id_rsa",
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAABlwAAAAdzc2gtcn",
		"user@h:~$ show version",
		"Cisco IOS XE Software",
		"",
	}, "\n")

	// Act
	got := string(RedactText([]byte(input)))

	// Assert
	if strings.Contains(got, "b3BlbnNzaC1rZXk") {
		t.Errorf("key material leaked:\n%s", got)
	}
	for _, want := range []string{"user@h:~$ show version", "Cisco IOS XE Software"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q was redacted after an unterminated PEM block:\n%s", want, got)
		}
	}
}

func TestRedactPEMBlockKeepsRedactingKeyBody(t *testing.T) {
	input := strings.Join([]string{
		"-----BEGIN RSA PRIVATE KEY-----",
		"Proc-Type: 4,ENCRYPTED",
		"DEK-Info: AES-128-CBC,0123456789ABCDEF",
		"",
		"MIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQDh",
		"exit",
		"-----END RSA PRIVATE KEY-----",
		"after",
	}, "\n")

	got := string(RedactText([]byte(input)))

	for _, secret := range []string{"Proc-Type", "DEK-Info", "MIIEvAIBADANBg", "exit"} {
		if strings.Contains(got, secret) {
			t.Errorf("%q inside a PEM block was not redacted:\n%s", secret, got)
		}
	}
	if !strings.HasSuffix(got, "\nafter") {
		t.Errorf("text after the END line was redacted:\n%s", got)
	}
}

func TestRedactPEMBlockGivesUpAfterLineLimit(t *testing.T) {
	lines := []string{"-----BEGIN PRIVATE KEY-----"}
	for i := 0; i < maxPEMBlockLines+5; i++ {
		lines = append(lines, "QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo")
	}
	lines = append(lines, "tail")

	got := strings.Split(string(RedactText([]byte(strings.Join(lines, "\n")))), "\n")

	if got[len(got)-1] != "tail" {
		t.Errorf("last row = %q; want it logged once the block exceeds its line limit", got[len(got)-1])
	}
}

func TestRedactNetworkDeviceSecrets(t *testing.T) {
	tests := []struct {
		line   string
		secret string
	}{
		{"enable secret 5 $1$mERr$hx5rVt7rPNoS4wqbXKX7m0", "$1$mERr$hx5rVt7rPNoS4wqbXKX7m0"},
		{"enable password cisco123", "cisco123"},
		{"username admin privilege 15 secret 9 $9$abcdefgh", "$9$abcdefgh"},
		{"username admin secret sha512 $6$saltsalt$hashhash", "$6$saltsalt$hashhash"},
		{"snmp-server community s3cr3tRO RO", "s3cr3tRO"},
		{"tacacs-server key 7 0822455D0A16", "0822455D0A16"},
		{"radius-server key MyRadiusKey", "MyRadiusKey"},
		{" key-string 7 094F471A1A0A", "094F471A1A0A"},
		{" neighbor 10.0.0.1 password 7 13061E010803", "13061E010803"},
		{" ip ospf message-digest-key 1 md5 7 060506324F41", "060506324F41"},
		{" ip ospf authentication-key OspfKey1", "OspfKey1"},
		{" password 7 02050D480809", "02050D480809"},
		{" key 7 070C285F4D06", "070C285F4D06"},
		{`    encrypted-password "$6$abc$def";`, "$6$abc$def"},
		{`    pre-shared-key ascii-text "$9$xyzxyz";`, "$9$xyzxyz"},
		{`    authentication-key "$9$qrsqrs";`, "$9$qrsqrs"},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			got := string(RedactText([]byte(tt.line)))
			if strings.Contains(got, tt.secret) {
				t.Errorf("RedactText(%q) = %q; secret not masked", tt.line, got)
			}
			if !strings.Contains(got, "[SECRET_REDACTED]") {
				t.Errorf("RedactText(%q) = %q; want a [SECRET_REDACTED] placeholder", tt.line, got)
			}
		})
	}
}

func TestRedactLeavesOrdinaryDeviceOutputAlone(t *testing.T) {
	for _, line := range []string{
		"Router#show ip interface brief",
		"key chain OSPF-KEYS",
		" key 1",
		"Password:",
		"% Bad passwords",
		"interface GigabitEthernet0/1",
	} {
		if got := string(RedactText([]byte(line))); got != line {
			t.Errorf("RedactText(%q) = %q; want unchanged", line, got)
		}
	}
}

func TestLoggerRedaction(t *testing.T) {
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host"}, true) // redactionEnabled = true
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// 1. AWS Access Key
	l.Write([]byte("my key is AKIA1234567890123456 here\n"))

	// 2. AWS Secret Key
	l.Write([]byte("export AWS_SECRET_ACCESS_KEY=\"abcd1234abcd1234abcd1234abcd1234abcd1234\"\n"))

	// 3. Bearer Token
	l.Write([]byte("Authorization: Bearer my_secret_jwt_token_123_abc\n"))

	// 4. SQL Password
	l.Write([]byte("ALTER USER 'ryu' IDENTIFIED BY 'super-secret-password';\n"))

	// 5. PEM Private Key (Stateful Block)
	l.Write([]byte("header info\n"))
	l.Write([]byte("-----BEGIN PRIVATE KEY-----\n"))
	l.Write([]byte("MIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQDh...\n"))
	l.Write([]byte("-----END PRIVATE KEY-----\n"))
	l.Write([]byte("footer info\n"))

	l.Close(0)

	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	content := string(data)

	// Asserts
	if strings.Contains(content, "AKIA1234567890123456") {
		t.Error("AWS Access Key was not redacted")
	}
	if !strings.Contains(content, "[AWS_ACCESS_KEY_REDACTED]") {
		t.Error("Missing AWS Access Key redacted placeholder")
	}

	if strings.Contains(content, "abcd1234abcd1234abcd1234abcd1234abcd1234") {
		t.Error("AWS Secret Key was not redacted")
	}
	if !strings.Contains(content, "[AWS_SECRET_KEY_REDACTED]") {
		t.Error("Missing AWS Secret Key redacted placeholder")
	}

	if strings.Contains(content, "my_secret_jwt_token_123_abc") {
		t.Error("Bearer Token was not redacted")
	}
	if !strings.Contains(content, "[TOKEN_REDACTED]") {
		t.Error("Missing Bearer Token redacted placeholder")
	}

	if strings.Contains(content, "super-secret-password") {
		t.Error("SQL Password was not redacted")
	}
	if !strings.Contains(content, "[PASSWORD_REDACTED]") {
		t.Error("Missing SQL Password redacted placeholder")
	}

	if strings.Contains(content, "MIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQDh") {
		t.Error("PEM Private Key data was not redacted")
	}
	if !strings.Contains(content, "[PEM PRIVATE KEY BLOCK STARTED - REDACTED]") {
		t.Error("Missing PEM start placeholder")
	}
	if !strings.Contains(content, "[PEM PRIVATE KEY DATA - REDACTED]") {
		t.Error("Missing PEM data placeholder")
	}
	if !strings.Contains(content, "[PEM PRIVATE KEY BLOCK ENDED - REDACTED]") {
		t.Error("Missing PEM end placeholder")
	}
}

func TestRedactEdgeCasesAndHeaderSanitization(t *testing.T) {
	// 1. Secret matching prefix keyword must still redact the secret, not the prefix.
	got := string(RedactText([]byte("Authorization: Bearer Bearer\nALTER USER u IDENTIFIED BY 'identified';\n")))
	if !strings.Contains(got, "Bearer [TOKEN_REDACTED]") {
		t.Errorf("Bearer keyword collision not redacted properly: %q", got)
	}
	if !strings.Contains(got, "IDENTIFIED BY '[PASSWORD_REDACTED]'") {
		t.Errorf("SQL keyword collision not redacted properly: %q", got)
	}

	// 2. Single-line PEM block must not leave inPEMBlock stuck true for subsequent lines.
	pemOneLine := "-----BEGIN PRIVATE KEY----- secret -----END PRIVATE KEY-----\nnormal line after\n"
	gotPEM := string(RedactText([]byte(pemOneLine)))
	if !strings.Contains(gotPEM, "normal line after") {
		t.Errorf("single-line PEM block left redactor stuck in PEM mode:\n%s", gotPEM)
	}

	// 3. Header command line must redact secrets when redactionEnabled=true and strip newlines.
	tmp := t.TempDir()
	l, err := New(tmp, []string{"ssh", "host", "Bearer secret_token_123\n# FakeHeader : injected"}, true)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	_ = l.Close(0)
	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "secret_token_123") {
		t.Errorf("header leaked unredacted token:\n%s", content)
	}
	if strings.Contains(content, "\n# FakeHeader :") {
		t.Errorf("header allowed newline injection:\n%s", content)
	}
}
