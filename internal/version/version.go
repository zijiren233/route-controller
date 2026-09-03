// Package version exposes build metadata injected by the linker.
package version

import "runtime"

var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

type Info struct {
	Version   string `json:"version"   yaml:"version"`
	GitCommit string `json:"gitCommit" yaml:"gitCommit"`
	BuildDate string `json:"buildDate" yaml:"buildDate"`
	GoVersion string `json:"goVersion" yaml:"goVersion"`
	Platform  string `json:"platform"  yaml:"platform"`
}

func Get() Info {
	return Info{
		Version:   Version,
		GitCommit: GitCommit,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
}
