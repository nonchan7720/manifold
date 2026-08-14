package version

import (
	"fmt"
)

var Version = "1.6.3" // x-release-please-version

var (
	MarkVersion = fmt.Sprintf("v%s", Version)
)
