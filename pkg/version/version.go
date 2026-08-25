package version

import (
	"fmt"
)

var Version = "1.6.5" // x-release-please-version

var (
	MarkVersion = fmt.Sprintf("v%s", Version)
)
