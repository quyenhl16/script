package report

import (
	"encoding/json"
	"regexp"
	"strings"
)

type checklistItem struct {
	Check, Status, Expected, Actual, Source, Details string
}

type checklistSection struct {
	Title, Phase, Subsection string
	Items                    []checklistItem
}

type checklistComponent struct {
	Component        string `json:"component"`
	Workload         string `json:"workload"`
	Status           string `json:"status"`
	Checks           int    `json:"checks"`
	Pass             int    `json:"pass"`
	Fail             int    `json:"fail"`
	Skip             int    `json:"skip"`
	Warn             int    `json:"warn"`
	Missing          int    `json:"missing"`
	Mismatch         int    `json:"mismatch"`
	Extra            int    `json:"extra"`
	ResolutionErrors int    `json:"resolution_errors"`
	Unresolved       int    `json:"unresolved"`
	WorkloadErrors   int    `json:"workload_errors"`
}

type checklistRecord struct {
	Type string `json:"type"`
	checklistComponent
}

type checklistOutput struct {
	Sections   []checklistSection
	Components []checklistComponent
	Messages   []string
	Counts     map[string]int
}

var (
	checklistStatusPattern  = regexp.MustCompile(`(?i)^\s*(.*?)\[(PASS|FAIL|FAILED|SKIP|SKIPPED|WARN|WARNING)\]\s*(?:\[([^]]+)\]\s*)?(.*)$`)
	checklistFieldPattern   = regexp.MustCompile(`(?i)(?:^|\s)(expected|actual|source|error)=`)
	separatorPattern        = regexp.MustCompile(`^[=*_-]{3,}$`)
	serviceHeadingPattern   = regexp.MustCompile(`(?i)^checking\s+service\s*:\s*(.+)$`)
	phaseHeadingPattern     = regexp.MustCompile(`(?i)^\[phase\s*([^]]*)\]\s*(.*)$`)
	subsectionPattern       = regexp.MustCompile(`^(\d+(?:\.\d+)+)\s+(.+)$`)
	scanningHeadingPattern  = regexp.MustCompile(`(?i)^scanning\s+pod\s+group\s*:\s*(.+?)(?:\s*\(.*)?$`)
	podHeadingPattern       = regexp.MustCompile(`(?i)^pod\s*:\s*\[([^]]+)\]$`)
	componentHeadingPattern = regexp.MustCompile(`(?i)^component\s*\[([^]]+)\]$`)
	entityPrefixPattern     = regexp.MustCompile(`(?i)^(service|pod|command)\s*\[([^]]+)\]\s*:?\s*(.*)$`)
	comparisonPathPattern   = regexp.MustCompile(`^([^\s/]+)/([^\s]+)(?:\s+(.*))?$`)
	completionPattern       = regexp.MustCompile(`(?i)^(?:checklist|dependency check|.* check) completed:`)
)

const checklistRecordPrefix = "SYSSETUP_REPORT "

func parseChecklistOutput(output string) checklistOutput {
	parsed := checklistOutput{Counts: map[string]int{"PASS": 0, "FAIL": 0, "SKIP": 0, "WARN": 0}}
	sectionIndex := make(map[string]int)
	currentPhase, currentSubsection, currentSection := "General", "", "General"
	lastSection, lastItem := -1, -1

	addItem := func(phase, subsection, section string, item checklistItem) {
		section = firstChecklistValue(section, subsection, phase, "General")
		key := strings.ToLower(strings.Join([]string{phase, subsection, section}, "\x00"))
		index, found := sectionIndex[key]
		if !found {
			index = len(parsed.Sections)
			sectionIndex[key] = index
			parsed.Sections = append(parsed.Sections, checklistSection{Title: section, Phase: strings.TrimSpace(phase), Subsection: strings.TrimSpace(subsection)})
		}
		parsed.Sections[index].Items = append(parsed.Sections[index].Items, item)
		parsed.Counts[item.Status]++
		lastSection, lastItem = index, len(parsed.Sections[index].Items)-1
	}

	for _, rawLine := range strings.Split(cleanOutput(output), "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, checklistRecordPrefix) {
			var record checklistRecord
			payload := strings.TrimSpace(strings.TrimPrefix(line, checklistRecordPrefix))
			if json.Unmarshal([]byte(payload), &record) == nil && record.Type == "component-summary" {
				record.Status = normalizeChecklistStatus(record.Status)
				parsed.Components = append(parsed.Components, record.checklistComponent)
			}
			continue
		}
		if line == "" || separatorPattern.MatchString(line) || completionPattern.MatchString(line) {
			continue
		}
		if match := serviceHeadingPattern.FindStringSubmatch(line); match != nil {
			currentSection = "Service: " + strings.TrimSpace(match[1])
			lastSection, lastItem = -1, -1
			continue
		}
		if match := phaseHeadingPattern.FindStringSubmatch(line); match != nil {
			currentPhase = "PHASE " + strings.TrimSpace(match[1])
			if label := strings.TrimSpace(match[2]); label != "" {
				currentPhase += " — " + label
			}
			currentSubsection, currentSection = "", ""
			lastSection, lastItem = -1, -1
			continue
		}
		if match := subsectionPattern.FindStringSubmatch(line); match != nil {
			currentSubsection = strings.TrimSpace(match[1] + " " + match[2])
			currentSection = ""
			lastSection, lastItem = -1, -1
			continue
		}
		if match := scanningHeadingPattern.FindStringSubmatch(line); match != nil {
			currentSection = "Pod group: " + strings.Trim(strings.TrimSpace(match[1]), "[]")
			lastSection, lastItem = -1, -1
			continue
		}
		if match := podHeadingPattern.FindStringSubmatch(line); match != nil {
			currentSection = "Pod: " + strings.TrimSpace(match[1])
			lastSection, lastItem = -1, -1
			continue
		}
		if match := componentHeadingPattern.FindStringSubmatch(line); match != nil {
			currentSection = "Component: " + strings.TrimSpace(match[1])
			lastSection, lastItem = -1, -1
			continue
		}

		match := checklistStatusPattern.FindStringSubmatch(line)
		if match == nil {
			if lastSection >= 0 && lastItem >= 0 && strings.HasPrefix(rawLine, " ") {
				item := &parsed.Sections[lastSection].Items[lastItem]
				item.Details = joinChecklistDetail(item.Details, line)
			} else if !looksLikeChecklistHeading(line) && !looksLikeChecklistMetadata(line, currentSection) {
				parsed.Messages = append(parsed.Messages, line)
			}
			continue
		}

		prefix, qualifier, body := strings.TrimSpace(match[1]), strings.TrimSpace(match[3]), strings.TrimSpace(match[4])
		if strings.HasPrefix(strings.ToLower(body), "summary:") {
			continue
		}
		fields, remaining := extractChecklistFields(body)
		section, check := checklistSectionAndCheck(currentSection, remaining)
		if prefix != "" {
			check = strings.TrimSuffix(prefix, ":") + ": " + check
		}
		item := checklistItem{Check: check, Status: normalizeChecklistStatus(match[2]), Expected: fields["expected"], Actual: fields["actual"], Source: fields["source"], Details: fields["error"]}
		if qualifier != "" {
			item.Details = joinChecklistDetail(strings.ToUpper(qualifier), item.Details)
		}
		normalizeChecklistItem(&item)
		addItem(currentPhase, currentSubsection, section, item)
	}

	if len(parsed.Components) > 0 {
		parsed.Counts = map[string]int{"PASS": 0, "FAIL": 0, "SKIP": 0, "WARN": 0}
		for _, component := range parsed.Components {
			parsed.Counts["PASS"] += component.Pass
			parsed.Counts["FAIL"] += component.Fail
			parsed.Counts["SKIP"] += component.Skip
			parsed.Counts["WARN"] += component.Warn
		}
	}
	return parsed
}

func firstChecklistValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "General"
}

func checklistComponentTotal(component checklistComponent) int {
	if component.Checks > 0 {
		return component.Checks
	}
	return component.Pass + component.Fail + component.Skip + component.Warn
}

func normalizeChecklistStatus(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "PASS", "DONE":
		return "PASS"
	case "FAIL", "FAILED", "CANCELLED":
		return "FAIL"
	case "SKIP", "SKIPPED", "PLANNED":
		return "SKIP"
	case "WARN", "WARNING", "PARTIAL":
		return "WARN"
	default:
		return strings.ToUpper(strings.TrimSpace(value))
	}
}

func extractChecklistFields(value string) (map[string]string, string) {
	fields := make(map[string]string)
	matches := checklistFieldPattern.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return fields, strings.TrimSpace(value)
	}
	remaining := strings.TrimSpace(value[:matches[0][0]])
	for index, match := range matches {
		end := len(value)
		if index+1 < len(matches) {
			end = matches[index+1][0]
		}
		name := strings.ToLower(value[match[2]:match[3]])
		fields[name] = trimChecklistValue(strings.TrimSpace(value[match[1]:end]))
	}
	return fields, remaining
}

func trimChecklistValue(value string) string {
	value = strings.TrimSuffix(strings.TrimSpace(value), ",")
	if len(value) >= 2 && ((value[0] == '[' && value[len(value)-1] == ']') || (value[0] == '(' && value[len(value)-1] == ')')) {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	return value
}

func checklistSectionAndCheck(current, value string) (string, string) {
	value = strings.TrimSpace(value)
	if match := entityPrefixPattern.FindStringSubmatch(value); match != nil {
		kind := strings.ToUpper(match[1][:1]) + strings.ToLower(match[1][1:])
		return kind + ": " + strings.TrimSpace(match[2]), strings.TrimSpace(match[3])
	}
	if match := comparisonPathPattern.FindStringSubmatch(value); match != nil {
		check := strings.TrimSpace(match[2])
		if suffix := strings.TrimSpace(match[3]); suffix != "" {
			check += " " + suffix
		}
		return "Component: " + strings.TrimSpace(match[1]), check
	}
	if value == "" {
		value = "Checklist item"
	}
	return current, value
}

func normalizeChecklistItem(item *checklistItem) {
	lower := strings.ToLower(item.Check)
	switch {
	case strings.HasPrefix(lower, "exists (") && strings.HasSuffix(item.Check, ")"):
		item.Details = joinChecklistDetail(item.Details, item.Check[len("exists ("):len(item.Check)-1])
		item.Check, item.Expected, item.Actual = "Exists", checklistValueOr(item.Expected, "Exists"), checklistValueOr(item.Actual, "Exists")
	case strings.Contains(lower, "is missing from namespace"):
		item.Check, item.Expected, item.Actual = "Exists in namespace", checklistValueOr(item.Expected, "Exists"), checklistValueOr(item.Actual, "Missing")
	case strings.HasPrefix(lower, "ready endpoint(s):"):
		addresses := strings.TrimSpace(item.Check[len("ready endpoint(s):"):])
		item.Check, item.Expected, item.Actual = "Ready endpoints", checklistValueOr(item.Expected, "At least 1"), checklistValueOr(item.Actual, addresses)
	case strings.Contains(lower, "no ready endpoint address was found"):
		item.Check, item.Expected, item.Actual = "Ready endpoints", checklistValueOr(item.Expected, "At least 1"), checklistValueOr(item.Actual, "0")
	case strings.Contains(lower, "has no tcp port") || strings.Contains(lower, "no tcp endpoint port was found"):
		item.Check, item.Expected, item.Actual = "TCP port", checklistValueOr(item.Expected, "Configured"), checklistValueOr(item.Actual, "Not configured")
	case strings.HasPrefix(lower, "external ip matches"):
		item.Check = "External IP"
		if item.Status == "PASS" {
			item.Actual = checklistValueOr(item.Actual, item.Expected)
		}
	case strings.HasPrefix(lower, "external ip mismatch"):
		item.Check = "External IP"
	case strings.HasPrefix(lower, "tcp ") && (strings.Contains(lower, " is reachable") || strings.Contains(lower, " is unreachable")):
		reachable := strings.Contains(lower, " is reachable")
		separator := " is unreachable"
		if reachable {
			separator = " is reachable"
		}
		target, detail, _ := strings.Cut(item.Check[len("TCP "):], separator)
		item.Check, item.Expected = "TCP connectivity: "+strings.TrimSpace(target), checklistValueOr(item.Expected, "Reachable")
		if reachable {
			item.Actual = checklistValueOr(item.Actual, "Reachable")
		} else {
			item.Actual = checklistValueOr(item.Actual, "Unreachable")
		}
		item.Details = joinChecklistDetail(item.Details, strings.Trim(strings.TrimSpace(detail), "()"))
	}
	item.Check = strings.TrimSpace(strings.TrimSuffix(item.Check, ":"))
	if item.Check == "" {
		item.Check = "Checklist item"
	}
}

func looksLikeChecklistHeading(value string) bool {
	upper := strings.ToUpper(value)
	if upper == value && len(value) > 3 || strings.HasPrefix(value, "Checking ") {
		return true
	}
	if prefix, _, found := strings.Cut(value, ":"); found {
		prefix = strings.TrimSpace(prefix)
		return prefix == strings.ToUpper(prefix) && strings.Contains(prefix, "CHECK")
	}
	return false
}

func looksLikeChecklistMetadata(value, currentSection string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(lower, "kubernetes ") || strings.HasPrefix(lower, "namespace=") || strings.HasPrefix(lower, "workbook=") || strings.HasPrefix(lower, "components=") {
		return true
	}
	return strings.HasPrefix(currentSection, "Component: ") && strings.Contains(value, "/")
}

func joinChecklistDetail(left, right string) string {
	left, right = strings.TrimSpace(left), strings.TrimSpace(right)
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	return left + "; " + right
}

func checklistValueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
