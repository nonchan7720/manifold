package version

import (
	"fmt"
)

var Version = "1.16.0" // x-release-please-version

var (
	MarkVersion = fmt.Sprintf("v%s", Version)
)
