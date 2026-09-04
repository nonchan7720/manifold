package version

import (
	"fmt"
)

var Version = "1.14.0" // x-release-please-version

var (
	MarkVersion = fmt.Sprintf("v%s", Version)
)
