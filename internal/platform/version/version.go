package version

// These values are replaced at build time with -ldflags.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// Info is the build metadata exposed by the API.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// Build returns the current build metadata.
func Build() Info {
	return Info{Version: Version, Commit: Commit, Date: Date}
}
