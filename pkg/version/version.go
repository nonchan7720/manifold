package version

import (
	"fmt"
)

var Version = "1.5.1" // x-release-please-version

var (
	MarkVersion = fmt.Sprintf("v%s", Version)
)
