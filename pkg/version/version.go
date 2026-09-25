package version

import (
	"fmt"
)

var Version = "1.15.1" // x-release-please-version

var (
	MarkVersion = fmt.Sprintf("v%s", Version)
)
