package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 使用真实 Makefile / Shell 脚本，替换外部 Docker / curl；真实镜像验收另见交付报告。
type workspace struct {
	t   *testing.T
	dir string
	env []string
}

func fixture(t *testing.T) *workspace {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"scripts", "fake-bin"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := []string{"Makefile", ".env.example", "compose.yaml", "compose.image.yaml", "compose.local.yaml"}
	scripts, err := filepath.Glob("*.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, script := range scripts {
		files = append(files, "scripts/"+script)
	}
	for _, file := range files {
		body, err := os.ReadFile(filepath.Join("..", file))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, file), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	f := &workspace{t: t, dir: dir, env: append(os.Environ(), "PATH="+filepath.Join(dir, "fake-bin")+":"+os.Getenv("PATH"), "COMMAND_LOG="+filepath.Join(dir, "commands"), "MINI_AGENT_STARTUP_TIMEOUT=1", "DOCKER=docker", "COMPOSE=docker compose")}
	f.write("fake-bin/docker", `#!/bin/sh
printf '%s\n' "$*" >> "$COMMAND_LOG"
case "$1" in
  info) exit 0 ;;
  inspect) printf '%s\n' "${FAKE_STATE:-running healthy}"; exit 0 ;;
esac
case " $* " in
  *' pull mini-agent '*) exit "${PULL_EXIT:-0}" ;;
  *' build mini-agent '*) exit "${BUILD_EXIT:-0}" ;;
  *' ps -a -q mini-agent '*) echo test-container ;;
esac
`, 0700)
	f.write("fake-bin/curl", `#!/bin/sh
[ "${HTTP_FAIL:-0}" = 0 ] || exit 7
case "$*" in
  *healthz*) echo '{"status":"ok"}' ;;
  *) [ "${PAGE_FAIL:-0}" = 0 ] || exit 22
     echo '<html><div id="root"></div></html>' ;;
esac
`, 0700)
	return f
}

func (f *workspace) write(name, body string, mode os.FileMode) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(body), mode); err != nil {
		f.t.Fatal(err)
	}
}
func (f *workspace) read(name string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, name))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(data)
}
func (f *workspace) run(ok bool, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("make", args...)
	cmd.Dir, cmd.Env = f.dir, f.env
	out, err := cmd.CombinedOutput()
	if (err == nil) != ok {
		f.t.Fatalf("make %v: err=%v\n%s", args, err, out)
	}
	return string(out)
}
func (f *workspace) configured(mode string) {
	f.run(true, "init", "MODE="+mode)
	f.write(".env", "MINI_AGENT_LLM_BASE_URL=http://127.0.0.1:9/v1\nMINI_AGENT_MODEL=offline-model\nMINI_AGENT_LLM_API_KEY=test-provider-secret\nMINI_AGENT_AUTH_TOKEN=test-http-secret\n", 0600)
}

func TestInitPreservesEnvAndSwitchesMode(t *testing.T) {
	f := fixture(t)
	f.run(true, "init", "MODE=image")
	if f.read(".env") != f.read(".env.example") {
		t.Fatal("initial env differs from template")
	}
	info, err := os.Stat(filepath.Join(f.dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("env mode: %o", info.Mode().Perm())
	}
	// 即使内容像 Shell 也只能保留，不能执行它或把值放进模式文件。
	secret := "MINI_AGENT_AUTH_TOKEN=private-token\nUNTRUSTED=$(touch should-not-exist)\n"
	f.write(".env", secret, 0600)
	out := f.run(true, "init", "MODE=local")
	if f.read(".env") != secret || f.read(".mini-agent-mode") != "local\n" {
		t.Fatal("reinit changed env or failed to save mode")
	}
	if strings.Contains(out, "private-token") {
		t.Fatal("init leaked token")
	}
	if _, err := os.Stat(filepath.Join(f.dir, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatal("env executed")
	}
	f.run(true, "init", "MODE=image")
	if f.read(".env") != secret || f.read(".mini-agent-mode") != "image\n" {
		t.Fatal("switch back failed")
	}
}

func TestInitRequiresExplicitModeWithoutTTY(t *testing.T) {
	f := fixture(t)
	out := f.run(false, "init", "MODE=")
	if !strings.Contains(out, "MODE=image") {
		t.Fatal("missing actionable non-interactive hint")
	}
	f.run(false, "init", "MODE=invalid")
	for _, name := range []string{".env", ".mini-agent-mode"} {
		if _, err := os.Stat(filepath.Join(f.dir, name)); !os.IsNotExist(err) {
			t.Fatalf("invalid init created %s", name)
		}
	}
}

func TestImageModePullsWithoutBuildingAndLocalBuildsWithoutPulling(t *testing.T) {
	for _, mode := range []string{"image", "local"} {
		t.Run(mode, func(t *testing.T) {
			f := fixture(t)
			f.configured(mode)
			out := f.run(true, "up")
			log := f.read("commands")
			if !strings.Contains(out, "started successfully") || !strings.Contains(out, "http://127.0.0.1:8080") || !strings.Contains(out, "Authorization: Bearer <your-token>") {
				t.Fatal("missing startup guidance")
			}
			for _, secret := range []string{"test-provider-secret", "test-http-secret"} {
				if strings.Contains(out+log, secret) {
					t.Fatal("secret leaked")
				}
			}
			prepare := " pull mini-agent"
			forbidden := " build mini-agent"
			if mode == "local" {
				prepare, forbidden = forbidden, prepare
			}
			if !strings.Contains(log, prepare) || strings.Contains(log, forbidden) {
				t.Fatalf("wrong source selected:\n%s", log)
			}
			start := " up -d --no-build --pull never mini-agent"
			if !strings.Contains(log, start) || strings.Index(log, prepare) > strings.Index(log, start) {
				t.Fatal("up ran before image was prepared")
			}
			if !strings.Contains(log, "-f compose."+mode+".yaml") {
				t.Fatal("mode override not used")
			}
			for _, command := range []string{"down", "logs", "ps"} {
				f.run(true, command)
			}
		})
	}
}

func TestUpFailureNeverReportsSuccess(t *testing.T) {
	for _, tc := range []struct{ name, mode, variable, want string }{
		{"pull", "image", "PULL_EXIT=1", "镜像拉取失败"},
		{"build", "local", "BUILD_EXIT=1", "镜像构建失败"},
		{"exited", "image", "FAKE_STATE=exited unhealthy", "容器状态"},
		// 关键回归：容器内部 Healthy 不足以证明宿主机页面与 API 可达。
		{"healthy-but-unreachable", "image", "HTTP_FAIL=1", "/healthz=unreachable"},
		{"healthy-api-without-page", "image", "PAGE_FAIL=1", "页面=unreachable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fixture(t)
			f.configured(tc.mode)
			f.env = append(f.env, tc.variable)
			out := f.run(false, "up")
			if strings.Contains(out, "started successfully") || !strings.Contains(out, tc.want) {
				t.Fatalf("wrong failure output: %s", out)
			}
			if (tc.name == "pull" || tc.name == "build") && strings.Contains(f.read("commands"), " up ") {
				t.Fatal("started after prepare failure")
			}
		})
	}
}

func TestUpRejectsMissingOrExecutableMode(t *testing.T) {
	f := fixture(t)
	f.run(false, "up")
	f.configured("image")
	f.write(".mini-agent-mode", "$(touch should-not-exist)\n", 0600)
	f.run(false, "up")
	if _, err := os.Stat(filepath.Join(f.dir, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatal("mode file executed")
	}
}

func TestExistingContainersCanBeManagedBeforeInit(t *testing.T) {
	f := fixture(t)
	for _, command := range []string{"down", "logs", "ps"} {
		f.run(true, command)
	}
	if strings.Contains(f.read("commands"), "compose.image.yaml") {
		t.Fatal("guessed mode before init")
	}
}
