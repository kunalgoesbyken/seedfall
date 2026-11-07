package seedfall

import "embed"

//go:embed docs/*.md skills/*/SKILL.md
var Files embed.FS
