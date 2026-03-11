package config

import (
	"flag"
	"time"
)

type Config struct {
	Port               int
	Host               string
	CollectRunningVMs  bool
	CollectStorage     bool
	MetricsPrefix      string
	LogLevel           string
	QMTerminalTimeout  time.Duration
	QMMaxTTL           time.Duration
	QMRand             time.Duration
	QMMonitorDeferClose bool
	ShowVersion        bool
}

func Parse() Config {
	c := Config{}
	flag.IntVar(&c.Port, "port", 9116, "HTTP server listen port")
	flag.StringVar(&c.Host, "host", "0.0.0.0", "HTTP server bind address")
	flag.BoolVar(&c.CollectRunningVMs, "collect-running-vms", true, "collect KVM VM metrics")
	flag.BoolVar(&c.CollectStorage, "collect-storage", true, "collect storage pool metrics")
	flag.StringVar(&c.MetricsPrefix, "metrics-prefix", "pve", "metric name prefix")
	flag.StringVar(&c.LogLevel, "loglevel", "INFO", "log level (DEBUG, INFO, WARNING, ERROR)")
	flag.DurationVar(&c.QMTerminalTimeout, "qm-terminal-timeout", 10*time.Second, "qm monitor command timeout")
	flag.DurationVar(&c.QMMaxTTL, "qm-max-ttl", 600*time.Second, "cache TTL for qm monitor data")
	flag.DurationVar(&c.QMRand, "qm-rand", 60*time.Second, "randomness for qm cache expiry")
	flag.BoolVar(&c.QMMonitorDeferClose, "qm-monitor-defer-close", true, "defer closing unresponsive qm sessions")
	flag.BoolVar(&c.ShowVersion, "version", false, "print the version and exit")
	flag.Parse()
	return c
}
