package version

import (
	"fmt"
)

var Version = "1.13.0" // x-release-please-version

var (
	MarkVersion = fmt.Sprintf("v%s", Version)
)
