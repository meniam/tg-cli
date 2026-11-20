package gigaam

// Artifact is one pinned download. Production code installs only Pinned();
// tests pass their own table that points at a local server.
type Artifact struct {
	// Component is ComponentRuntime, ComponentModel or ComponentVAD.
	Component string
	// URL is the exact address to download.
	URL string
	// Size is the exact size in bytes of the downloaded file.
	Size int64
	// SHA256 is the lower-case hex digest of the downloaded file.
	SHA256 string
	// TopDir is the only top-level directory allowed in an archive. Empty
	// for a plain file (ComponentVAD), which is not unpacked.
	TopDir string
	// Keep lists what is extracted from TopDir: a file name, or a name ending
	// in "/" for a whole directory. Other entries are checked and skipped.
	Keep []string
	// MaxUnpacked caps the sum of unpacked file sizes declared in the archive.
	MaxUnpacked int64
}

// Pinned is the single table of artifacts checked by the manager on
// 2026-11-20 (sizes match the GitHub API, hashes computed after download).
// To update a version, edit this table and nothing else.
func Pinned() []Artifact {
	return []Artifact{
		{
			Component:   ComponentRuntime,
			URL:         "https://github.com/k2-fsa/sherpa-onnx/releases/download/v2.0.1/sherpa-onnx-v2.0.1-osx-arm64-shared-no-tts.tar.bz2",
			Size:        18663994,
			SHA256:      "4fcc6c77c8d8d2e2f04cfdc17c6fd224c01469b78529370eb9d8e0e5ec97ac79",
			TopDir:      "sherpa-onnx-v2.0.1-osx-arm64-shared-no-tts",
			Keep:        []string{"bin/", "lib/"},
			MaxUnpacked: 256 << 20,
		},
		{
			Component: ComponentModel,
			URL:       "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-nemo-transducer-punct-giga-am-v3-russian-2025-12-16.tar.bz2",
			Size:      170197019,
			SHA256:    "f9620a0099019c6afcee26525ef9ed3297fa50dd5691c1902af0c948fc1a470b",
			TopDir:    "sherpa-onnx-nemo-transducer-punct-giga-am-v3-russian-2025-12-16",
			Keep: []string{
				"encoder.int8.onnx", "decoder.onnx", "joiner.onnx", "tokens.txt", "LICENSE",
			},
			MaxUnpacked: 512 << 20,
		},
		{
			Component: ComponentVAD,
			URL:       "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/silero_vad.onnx",
			Size:      643854,
			SHA256:    "9e2449e1087496d8d4caba907f23e0bd3f78d91fa552479bb9c23ac09cbb1fd6",
		},
	}
}
