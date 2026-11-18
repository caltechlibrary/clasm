package mpreflight

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/caltechlibrary/clasm/internal/executor"
)

// TestLive_ChecksOnARealVM runs the real scripts under Ubuntu's own awk and
// shell against files it creates in /var/tmp/clasm-pf-test on the VM, with fake
// values. It uses no production data. It is skipped unless
// CLASM_LIVE_MULTIPASS_VM names a running throwaway VM.
func TestLive_ChecksOnARealVM(t *testing.T) {
	vm := os.Getenv("CLASM_LIVE_MULTIPASS_VM")
	if vm == "" {
		t.Skip("set CLASM_LIVE_MULTIPASS_VM to a throwaway VM to run this")
	}
	ctx := context.Background()
	mp := executor.NewMultipassExecutor(vm)
	sh := func(cmd string) string {
		out, _, err := mp.Run(ctx, cmd, time.Minute, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	const base = "/var/tmp/clasm-pf-test"
	sh("rm -rf " + base + " && mkdir -p " + base + "/site " + base + "/home/ubuntu/.aws " + base + "/home/nocreds")
	defer sh("rm -rf " + base)
	sh("printf 'FOO=1\\nINVENIO_MAIL_SUPPRESS_SEND=False\\nINVENIO_S3_SECRET_ACCESS_KEY=FAKEVALUE0123\\n' > " + base + "/site/.env; chmod 600 " + base + "/site/.env")
	sh("printf '[default]\\naws_access_key_id = FAKEVALUE0123\\n' > " + base + "/home/ubuntu/.aws/credentials")

	o := Options{EnvGlobs: []string{base + "/site/.env"}, HomeGlobs: []string{base + "/home/*"}, CredentialGlobs: []string{base + "/none"}, Docker: true, DiskPath: "/var/tmp", DiskNeed: 1 << 20}
	findings, err := Run(ctx, mp, o)
	if err == nil {
		t.Fatalf("want a refusal, got %+v", findings)
	}
	mail, creds, disk := findings[0], findings[1], findings[2]
	if mail.OK || creds.OK || !disk.OK {
		t.Errorf("mail=%+v creds=%+v disk=%+v", mail, creds, disk)
	}
	for _, want := range []string{"INVENIO_S3_SECRET_ACCESS_KEY", "aws_access_key_id", "credentials"} {
		if !strings.Contains(creds.Detail, want) {
			t.Errorf("creds finding missing %q: %s", want, creds.Detail)
		}
	}
	if strings.Contains(creds.Detail, "FAKEVALUE0123") {
		t.Error("a value leaked")
	}

	o.SetMailSuppression = true
	sh("sed -i '/SECRET_ACCESS/d' " + base + "/site/.env; rm -f " + base + "/home/ubuntu/.aws/credentials")
	findings, err = Run(ctx, mp, o)
	if err != nil {
		t.Fatalf("after cleaning: %v %+v", err, findings)
	}
	if got := sh("cat " + base + "/site/.env; ls -A " + base + "/site; stat -c %a " + base + "/site/.env"); !strings.Contains(got, "INVENIO_MAIL_SUPPRESS_SEND=True") || !strings.Contains(got, ".env.clasm-bak") || !strings.Contains(got, "600") {
		t.Errorf("after set: %s", got)
	}
	if f, err := CheckDiskSpace(ctx, mp, "/var/tmp", 1<<60); err != nil || f.OK {
		t.Errorf("an exabyte must not fit: %+v %v", f, err)
	}
}
