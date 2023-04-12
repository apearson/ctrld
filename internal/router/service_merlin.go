package router

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/template"

	"github.com/kardianos/service"
)

const merlinJFFSScriptPath = "/jffs/scripts/services-start"

type merlinSvc struct {
	i        service.Interface
	platform string
	*service.Config
}

func newMerlinService(i service.Interface, platform string, c *service.Config) (service.Service, error) {
	s := &merlinSvc{
		i:        i,
		platform: platform,
		Config:   c,
	}
	return s, nil
}

func (s *merlinSvc) String() string {
	if len(s.DisplayName) > 0 {
		return s.DisplayName
	}
	return s.Name
}

func (s *merlinSvc) Platform() string {
	return s.platform
}

func (s *merlinSvc) configPath() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	return path + ".startup"
}

func (s *merlinSvc) template() *template.Template {
	return template.Must(template.New("").Parse(merlinSvcScript))
}

func (s *merlinSvc) Install() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	if !strings.HasPrefix(exePath, "/jffs/") {
		return errors.New("could not install service outside /jffs")
	}
	if _, err := nvram("set", "jffs2_scripts=1"); err != nil {
		return err
	}
	if _, err := nvram("commit"); err != nil {
		return err
	}

	confPath := s.configPath()
	if _, err := os.Stat(confPath); err == nil {
		return fmt.Errorf("already installed: %s", confPath)
	}

	var to = &struct {
		*service.Config
		Path string
	}{
		s.Config,
		exePath,
	}

	f, err := os.Create(confPath)
	if err != nil {
		return fmt.Errorf("os.Create: %w", err)
	}
	defer f.Close()

	if err := s.template().Execute(f, to); err != nil {
		return fmt.Errorf("s.template.Execute: %w", err)
	}

	if err = os.Chmod(confPath, 0755); err != nil {
		return fmt.Errorf("os.Chmod: startup script: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(merlinJFFSScriptPath), 0755); err != nil {
		return fmt.Errorf("os.MkdirAll: %w", err)
	}
	if _, err := os.Stat(merlinJFFSScriptPath); os.IsNotExist(err) {
		if err := os.WriteFile(merlinJFFSScriptPath, []byte("#!/bin/sh\n"), 0755); err != nil {
			return err
		}
	}
	if err := os.Chmod(merlinJFFSScriptPath, 0755); err != nil {
		return fmt.Errorf("os.Chmod: jffs script: %w", err)
	}

	tmpScript, err := os.CreateTemp("", "ctrld_install")
	if err != nil {
		return fmt.Errorf("os.CreateTemp: %w", err)
	}
	defer os.Remove(tmpScript.Name())
	defer tmpScript.Close()

	if _, err := tmpScript.WriteString(merlinAddStartupScript); err != nil {
		return fmt.Errorf("tmpScript.WriteString: %w", err)
	}
	if err := tmpScript.Close(); err != nil {
		return fmt.Errorf("tmpScript.Close: %w", err)
	}
	if err := exec.Command("sh", tmpScript.Name(), s.configPath()+" start", merlinJFFSScriptPath).Run(); err != nil {
		return fmt.Errorf("exec.Command: add startup script: %w", err)
	}

	return nil
}

func (s *merlinSvc) Uninstall() error {
	if err := os.Remove(s.configPath()); err != nil {
		return fmt.Errorf("os.Remove: %w", err)
	}
	tmpScript, err := os.CreateTemp("", "ctrld_uninstall")
	if err != nil {
		return fmt.Errorf("os.CreateTemp: %w", err)
	}
	defer os.Remove(tmpScript.Name())
	defer tmpScript.Close()

	if _, err := tmpScript.WriteString(merlinRemoveStartupScript); err != nil {
		return fmt.Errorf("tmpScript.WriteString: %w", err)
	}
	if err := tmpScript.Close(); err != nil {
		return fmt.Errorf("tmpScript.Close: %w", err)
	}
	if err := exec.Command("sh", tmpScript.Name(), s.configPath()+" start", merlinJFFSScriptPath).Run(); err != nil {
		return fmt.Errorf("exec.Command: %w", err)
	}
	return nil
}

func (s *merlinSvc) Logger(errs chan<- error) (service.Logger, error) {
	if service.Interactive() {
		return service.ConsoleLogger, nil
	}
	return s.SystemLogger(errs)
}

func (s *merlinSvc) SystemLogger(errs chan<- error) (service.Logger, error) {
	return newSysLogger(s.Name, errs)
}

func (s *merlinSvc) Run() (err error) {
	err = s.i.Start(s)
	if err != nil {
		return err
	}

	if interactice, _ := isInteractive(); !interactice {
		signal.Ignore(syscall.SIGHUP)
		signal.Ignore(sigCHLD)
	}

	var sigChan = make(chan os.Signal, 3)
	signal.Notify(sigChan, syscall.SIGTERM, os.Interrupt)
	<-sigChan

	return s.i.Stop(s)
}

func (s *merlinSvc) Status() (service.Status, error) {
	if _, err := os.Stat(s.configPath()); os.IsNotExist(err) {
		return service.StatusUnknown, service.ErrNotInstalled
	}
	out, err := exec.Command(s.configPath(), "status").CombinedOutput()
	if err != nil {
		return service.StatusUnknown, err
	}
	switch string(bytes.TrimSpace(out)) {
	case "running":
		return service.StatusRunning, nil
	default:
		return service.StatusStopped, nil
	}
}

func (s *merlinSvc) Start() error {
	return exec.Command(s.configPath(), "start").Run()
}

func (s *merlinSvc) Stop() error {
	return exec.Command(s.configPath(), "stop").Run()
}

func (s *merlinSvc) Restart() error {
	err := s.Stop()
	if err != nil {
		return err
	}
	return s.Start()
}

const merlinSvcScript = `#!/bin/sh

name="{{.Name}}"
cmd="{{.Path}}{{range .Arguments}} {{.}}{{end}}"
pid_file="/tmp/$name.pid"

get_pid() {
  cat "$pid_file"
}

is_running() {
  [ -f "$pid_file" ] && ps | grep -q "^ *$(get_pid) "
}

case "$1" in
  start)
    if is_running; then
      logger -c "Already started"
    else
      logger -c "Starting $name"
      if [ -f /rom/ca-bundle.crt ]; then
        # For John’s fork
        export SSL_CERT_FILE=/rom/ca-bundle.crt
      fi
      $cmd &
      echo $! > "$pid_file"
      chmod 600 "$pid_file"
      if ! is_running; then
       logger -c "Failed to start $name"
       exit 1
      fi
    fi
  ;;
  stop)
    if is_running; then
      logger -c "Stopping $name..."
      kill "$(get_pid)"
      for _ in 1 2 3 4 5; do
        if ! is_running; then
          logger -c "stopped"
          if [ -f "$pid_file" ]; then
            rm "$pid_file"
          fi
          exit 0
        fi
        printf "."
        sleep 2
      done
      logger -c "failed to stop $name"
      exit 1
    fi
    exit 1
  ;;
  restart)
    $0 stop
    $0 start
  ;;
  status)
    if is_running; then
      echo "running"
    else
      echo "stopped"
      exit 1
    fi
  ;;
  *)
    echo "Usage: $0 {start|stop|restart|status}"
    exit 1
  ;;
esac
exit 0
`

const merlinAddStartupScript = `#!/bin/sh

line=$1
file=$2

. /usr/sbin/helper.sh

pc_append "$line" "$file" 
`

const merlinRemoveStartupScript = `#!/bin/sh

line=$1
file=$2

. /usr/sbin/helper.sh

pc_delete "$line" "$file" 
`
