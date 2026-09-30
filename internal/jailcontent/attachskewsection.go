package jailcontent

import (
	"strings"
)

// attachskewsection.go renders the ACKNOWLEDGED-ATTACH section of the briefing
// (docs/design/attach-skew-and-contract-guardrails.md, OQ-SK4 decided as SK-D15).
//
// An attach whose running jail cannot receive what the entry delivers never proceeds on its own
// (OQ-SK1): it asks for a restart at a terminal and refuses elsewhere, and only
// YOLO_ALLOW_ATTACH_SKEW lets it go ahead, delivering nothing of the entry's provider/profile
// channel. The rulings make that acknowledged attach loud, and its stderr account scrolls away the
// moment the agent's screen takes the terminal. The briefing is what the session it starts reads,
// and the host writes it on every attach, into the running jail's staging, so even a jail whose
// own binaries predate this section shows it. So the attach puts the same account here: the
// version the jail was launched with, the contract tags it lacks, what else differs, and what was
// withheld. Rendered only for such an attach; every other entry's briefing has no section.

// AttachSkew is what an attach that proceeded under YOLO_ALLOW_ATTACH_SKEW knows about the jail it
// entered. Names only, never a value: the briefing is readable by everything in the jail.
type AttachSkew struct {
	// JailVersion is the yolo version the jail was launched with, "" when its environment does not
	// record one.
	JailVersion string
	// LauncherVersion is the version of the yolo that attached, "" when unknown.
	LauncherVersion string
	// Jail completes "This jail …": what is wrong with it for the entry that started this session.
	Jail string
	// MissingTags are the contract tags the jail lacks and this entry needed.
	MissingTags []AttachSkewTag
	// Differences are the other ways it could not take the entry, one plain sentence each.
	Differences []string
	// Withheld names what the entry would have delivered and did not.
	Withheld []string
	// Remedy is the host command that ends the difference, as the attach's own message names it.
	Remedy string
}

// AttachSkewTag is one contract tag a jail lacks: the name a launch freezes into
// YOLO_CONTRACT_TAGS, and what a jail without it cannot do.
type AttachSkewTag struct {
	Tag   string
	Lacks string
}

// attachSkewSection renders the section, or nothing for a nil skew.
func attachSkewSection(s *AttachSkew) []string {
	if s == nil {
		return nil
	}
	lines := []string{
		"## ⚠ This session runs in a jail that could not take what started it",
		"",
		"The attach that started this session found that this jail " + strings.TrimSuffix(s.Jail, ".") +
			", and went ahead only because `YOLO_ALLOW_ATTACH_SKEW` was set. Nothing of its " +
			"provider/profile selection was delivered: this jail keeps the environment its last " +
			"entry gave it.",
		"",
	}
	version := "an older yolo, whose version this jail does not record"
	if s.JailVersion != "" {
		version = "yolo " + s.JailVersion
	}
	if s.LauncherVersion != "" && s.LauncherVersion != s.JailVersion {
		version += " (the attach was yolo " + s.LauncherVersion + ")"
	}
	lines = append(lines, "- **The jail was launched with**: "+version+".")
	for _, t := range s.MissingTags {
		lines = append(lines, "- **It lacks the `"+t.Tag+"` contract**: "+strings.TrimSuffix(t.Lacks, ".")+".")
	}
	for _, d := range s.Differences {
		lines = append(lines, "- "+strings.TrimSuffix(d, ".")+".")
	}
	if len(s.Withheld) > 0 {
		lines = append(lines, "- **Withheld from this session**:")
		for _, w := range s.Withheld {
			lines = append(lines, "  - "+w)
		}
	}
	remedy := "a restart on the host"
	if s.Remedy != "" {
		remedy = s.Remedy + ", then a new launch"
	}
	lines = append(lines, "",
		"So a credential, profile or setting this session was meant to have may be missing, and a "+
			"yolo feature newer than the jail may fail here. It is not a fault in the workspace. "+
			"Ending the difference takes "+remedy+", which ends every session in this jail: tell the "+
			"user rather than work around it.",
		"")
	return lines
}
