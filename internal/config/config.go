package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Device struct {
	Root      string
	AutoMount *bool
}
type Config struct {
	BaseDir        string
	AutoMount      bool
	CheckInterval  time.Duration
	ConnectTimeout time.Duration
	MaxRetries     int
	LogLevel       string
	Devices        map[string]Device
}

func Defaults() Config {
	h, _ := os.UserHomeDir()
	return Config{BaseDir: filepath.Join(h, "Android"), AutoMount: true, CheckInterval: 5 * time.Minute, ConnectTimeout: 20 * time.Second, MaxRetries: 5, LogLevel: "info", Devices: map[string]Device{}}
}
func Path() string {
	d, ok := os.LookupEnv("XDG_CONFIG_HOME")
	if !ok || d == "" {
		h, _ := os.UserHomeDir()
		d = filepath.Join(h, ".config")
	}
	return filepath.Join(d, "gsconnect-mount-manager", "config.toml")
}

// Load intentionally supports the small documented TOML subset, with no credentials fields.
func Load(path string) (Config, error) {
	c := Defaults()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	defer f.Close()
	section := ""
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(strings.SplitN(s.Text(), "#", 2)[0])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[device.") && strings.HasSuffix(line, "]") {
			section = strings.Trim(strings.TrimSuffix(strings.TrimPrefix(line, "[device."), "]"), "\"")
			if _, ok := c.Devices[section]; !ok {
				c.Devices[section] = Device{}
			}
			continue
		}
		p := strings.SplitN(line, "=", 2)
		if len(p) != 2 {
			continue
		}
		k := strings.TrimSpace(p[0])
		v := strings.Trim(strings.TrimSpace(p[1]), "\"")
		if section != "" {
			d := c.Devices[section]
			switch k {
			case "root":
				d.Root = v
			case "automount":
				b, e := strconv.ParseBool(v)
				if e == nil {
					d.AutoMount = &b
				}
			}
			c.Devices[section] = d
			continue
		}
		switch k {
		case "base_dir":
			c.BaseDir = v
		case "automount":
			c.AutoMount, _ = strconv.ParseBool(v)
		case "check_interval":
			if x, e := time.ParseDuration(v); e == nil {
				c.CheckInterval = x
			}
		case "connect_timeout":
			if x, e := time.ParseDuration(v); e == nil {
				c.ConnectTimeout = x
			}
		case "max_retries":
			c.MaxRetries, _ = strconv.Atoi(v)
		case "log_level":
			c.LogLevel = v
		}
	}
	if err := s.Err(); err != nil {
		return c, err
	}
	if strings.HasPrefix(c.BaseDir, "~/") {
		h, _ := os.UserHomeDir()
		c.BaseDir = filepath.Join(h, strings.TrimPrefix(c.BaseDir, "~/"))
	}
	if c.CheckInterval < time.Minute {
		c.CheckInterval = time.Minute
	}
	if c.MaxRetries < 0 {
		c.MaxRetries = 0
	}
	return c, nil
}
