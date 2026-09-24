package report

import (
	"regexp"
	"strings"
)

type checklistItem struct {
	Check    string
	Status   string
	Expected string
	Actual   string
	Source   string
	Details  string
}

type checklistSection struct {
	Title string
	Items []checklistItem
}

type checklistOutput struct {
	Sections []checklistSection
	Messages []string
	Counts   map[string]int
}

var (
	checklistStatusPattern = regexp.MustCompile(`^\s*\[(PASS|FAIL|FAILED|SKIP|SKIPPED|WARN|WARNING)\]\s*(?:\[([^]]+)\]\s*)?(.*)$`)
	checklistFieldPattern  = regexp.MustCompile(`(?i)(?:^|\s)(expected|actual|source|error)=`)
	separatorPattern       = regexp.MustCompile(`^[=*_\-]{3,}$`)
	serviceHeadingPattern  = regexp.MustCompile(`(?i)^checking\s+service\s*:\s*(.+)$`)
	phaseHeadingPattern    = regexp.MustCompile(`(?i)^\[phase\s*([^]]*)\]\s*(.*)$`)
	scanningHeadingPattern = regexp.MustCompile(`(?i)^scanning\s+pod\s+group\s*:\s*(.+?)(?:\s*\(.*)?$`)
	entityPrefixPattern    = regexp.MustCompile(`(?i)^(service|pod|command)\s*\[([^]]+)\]\s*:?\s*(.*)$`)
	comparisonPathPattern  = regexp.MustCompile(`^([^\s/]+)/([^\s]+)(?:\s+(.*))?$`)
	completionPattern      = regexp.MustCompile(`(?i)^(?:checklist|dependency check|.* check) completed:`)
)

func parseChecklistOutput(output string) checklistOutput {
	parsed := checklistOutput{Counts: map[string]int{"PASS": 0, "FAIL": 0, "SKIP": 0, "WARN": 0}}
	sectionIndex := make(map[string]int)
	currentSection := "General"
	lastSection := -1
	lastItem := -1

	addItem := func(section string, item checklistItem) {
		section = strings.TrimSpace(section)
		if section == "" {
			section = "General"
		}
		key := strings.ToLower(section)
		index, found := sectionIndex[key]
		if !found {
			index = len(parsed.Sections)
			sectionIndex[key] = index
			parsed.Sections = append(parsed.Sections, checklistSection{Title: section})
		}
		parsed.Sections[index].Items = append(parsed.Sections[index].Items, item)
		parsed.Counts[item.Status]++
		lastSection = index
		lastItem = len(parsed.Sections[index].Items) - 1
	}

	for _, rawLine := range strings.Split(cleanOutput(output), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || separatorPattern.MatchString(line) || completionPattern.MatchString(line) {
			continue
		}
		if match := serviceHeadingPattern.FindStringSubmatch(line); match != nil {
			currentSection = "Service: " + strings.TrimSpace(match[1])
			lastSection, lastItem = -1, -1
			continue
		}
		if match := phaseHeadingPattern.FindStringSubmatch(line); match != nil {
			label := strings.TrimSpace(match[2])
			if label == "" {
				label = "Phase " + strings.TrimSpace(match[1])
			}
			currentSection = label
			lastSection, lastItem = -1, -1
			continue
		}
		if match := scanningHeadingPattern.FindStringSubmatch(line); match != nil {
			currentSection = "Pod group: " + strings.Trim(strings.TrimSpace(match[1]), "[]")
			lastSection, lastItem = -1, -1
			continue
		}

		match := checklistStatusPattern.FindStringSubmatch(line)
		if match == nil {
			if lastSection >= 0 && lastItem >= 0 && strings.HasPrefix(rawLine, " ") {
				item := &parsed.Sections[lastSection].Items[lastItem]
				item.Details = joinChecklistDetail(item.Details, line)
			} else if !looksLikeChecklistHeading(line) {
				parsed.Messages = append(parsed.Messages, line)
			}
			continue
		}

		status := normalizeChecklistStatus(match[1])
		qualifier := strings.TrimSpace(match[2])
		body := strings.TrimSpace(match[3])
		fields, remaining := extractChecklistFields(body)
		section, check := checklistSectionAndCheck(currentSection, remaining)
		item := checklistItem{
			Check: check, Status: status, Expected: fields["expected"], Actual: fields["actual"],
			Source: fields["source"], Details: fields["error"],
		}
		if qualifier != "" {
			item.Details = joinChecklistDetail(strings.ToUpper(qualifier), item.Details)
		}
		normalizeChecklistItem(&item)
		addItem(section, item)
	}

	return parsed
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
	remainingParts := make([]string, 0, len(matches)+1)
	remainingParts = append(remainingParts, strings.TrimSpace(value[:matches[0][0]]))
	for index, match := range matches {
		end := len(value)
		if index+1 < len(matches) {
			end = matches[index+1][0]
		}
		name := strings.ToLower(value[match[2]:match[3]])
		fieldValue := strings.TrimSpace(value[match[1]:end])
		fields[name] = trimChecklistValue(fieldValue)
	}
	return fields, strings.TrimSpace(strings.Join(nonEmptyStrings(remainingParts), " "))
}

func trimChecklistValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, ",")
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
		section := "Component: " + strings.TrimSpace(match[1])
		check := strings.TrimSpace(match[2])
		if suffix := strings.TrimSpace(match[3]); suffix != "" {
			check += " " + suffix
		}
		return section, check
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
		item.Check = "Exists"
		item.Expected = checklistValueOr(item.Expected, "Exists")
		item.Actual = checklistValueOr(item.Actual, "Exists")
	case strings.Contains(lower, "is missing from namespace"):
		item.Check = "Exists in namespace"
		item.Expected = checklistValueOr(item.Expected, "Exists")
		item.Actual = checklistValueOr(item.Actual, "Missing")
	case strings.HasPrefix(lower, "ready endpoint(s):"):
		addresses := strings.TrimSpace(item.Check[len("ready endpoint(s):"):])
		item.Check = "Ready endpoints"
		item.Expected = checklistValueOr(item.Expected, "At least 1")
		item.Actual = checklistValueOr(item.Actual, addresses)
	case strings.Contains(lower, "no ready endpoint address was found"):
		item.Check = "Ready endpoints"
		item.Expected = checklistValueOr(item.Expected, "At least 1")
		item.Actual = checklistValueOr(item.Actual, "0")
	case strings.Contains(lower, "has no tcp port") || strings.Contains(lower, "no tcp endpoint port was found"):
		item.Check = "TCP port"
		item.Expected = checklistValueOr(item.Expected, "Configured")
		item.Actual = checklistValueOr(item.Actual, "Not configured")
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
		item.Check = "TCP connectivity: " + strings.TrimSpace(target)
		item.Expected = checklistValueOr(item.Expected, "Reachable")
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

func nonEmptyStrings(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	return result
}
