package config

import "time"

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
