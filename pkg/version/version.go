package version

import (
	"fmt"
)

var Version = "1.15.0" // x-release-please-version

var (
	MarkVersion = fmt.Sprintf("v%s", Version)
)
