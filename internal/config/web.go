package config

import (
	"errors"
	"net"
	"strconv"
	"time"
)

// Web configures the local web interface.
type Web struct {
	CacheBytes int64  `toml:"cache_bytes" json:"cache_bytes"`
	CacheTTL   string `toml:"cache_ttl" json:"cache_ttl"`
	Listen     string `toml:"listen" json:"listen"`
	Open       bool   `toml:"open" json:"open"`
}

func defaultWeb() Web {
	return Web{CacheBytes: 32 << 20, CacheTTL: "168h"}
}

func (w Web) TTL() (time.Duration, error) {
	ttl, err := time.ParseDuration(w.CacheTTL)
	if err != nil || ttl <= 0 {
		return 0, invalid("web.cache_ttl must be a positive duration")
	}
	if w.CacheBytes < 128<<10 || w.CacheBytes > 1<<30 {
		return 0, invalid("web.cache_bytes must be between 131072 and 1073741824")
	}
	return ttl, nil
}

// ValidateWebListen checks the viewer address and whether it can be used without a flag.
func ValidateWebListen(address string, explicit bool) (bool, error) {
	if address == "" {
		return false, nil
	}
	host, port, err := net.SplitHostPort(address)
	n, parseErr := strconv.Atoi(port)
	ip := net.ParseIP(host)
	if err != nil || parseErr != nil || n < 1 || n > 65535 || (host != "localhost" && (ip == nil || ip.IsUnspecified())) {
		return false, errors.New("listen must be a literal IP address or localhost with a port")
	}
	remote := host != "localhost" && !ip.IsLoopback()
	if remote && !explicit {
		return false, errors.New("a non-loopback address requires an explicit --listen flag")
	}
	return remote, nil
}
