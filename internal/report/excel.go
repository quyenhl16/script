package report

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	excelMaxCellRunes = 32000
	excelMaxRows      = 1048576
)

type excelCell struct {
	column int
	value  string
	style  int
	number bool
}

type excelRow struct {
	index  int
	height float64
	cells  []excelCell
}

type excelSheet struct {
	name       string
	rows       []excelRow
	merges     []string
	columns    []float64
	freezeRows int
	autoFilter string
}

func renderExcel(document Document) ([]byte, error) {
	sheets := buildExcelSheets(document)
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	files := []struct {
		name    string
		content string
	}{
		{"[Content_Types].xml", excelContentTypes(len(sheets))},
		{"_rels/.rels", excelRootRelationships},
		{"xl/workbook.xml", excelWorkbook(sheets)},
		{"xl/_rels/workbook.xml.rels", excelWorkbookRelationships(len(sheets))},
		{"xl/styles.xml", excelStyles},
	}
	for index, sheet := range sheets {
		files = append(files, struct {
			name    string
			content string
		}{fmt.Sprintf("xl/worksheets/sheet%d.xml", index+1), excelWorksheet(sheet)})
	}
	for _, item := range files {
		writer, err := archive.Create(item.name)
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write([]byte(item.content)); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func buildExcelSheets(document Document) []excelSheet {
	names := excelSheetNames(document.Entries)
	sheets := []excelSheet{buildExcelSummary(document, names)}
	for index, entry := range document.Entries {
		sheets = append(sheets, buildExcelEntry(entry, names[index]))
	}
	return sheets
}

func buildExcelSummary(document Document, entrySheets []string) excelSheet {
	sheet := excelSheet{
		name: "Summary", columns: []float64{24, 42, 24, 14, 12, 12, 12, 12, 12, 18, 56}, freezeRows: 1,
		merges: []string{"A1:K1"},
	}
	sheet.rows = append(sheet.rows,
		excelRow{index: 1, height: 28, cells: []excelCell{{column: 1, value: document.Title, style: 1}}},
		excelRow{index: 3, cells: []excelCell{{1, "Status", 3, false}, {2, document.Status, excelStatusStyle(document.Status), false}}},
		excelRow{index: 4, cells: []excelCell{{1, "System", 3, false}, {2, valueOrDash(document.System), 4, false}}},
		excelRow{index: 5, cells: []excelCell{{1, "Profile", 3, false}, {2, valueOrDash(document.Profile), 4, false}}},
		excelRow{index: 6, cells: []excelCell{{1, "Type", 3, false}, {2, valueOrDash(document.Kind), 4, false}}},
		excelRow{index: 7, cells: []excelCell{{1, "Started", 3, false}, {2, excelDateValue(document.StartedAt), 10, true}}},
		excelRow{index: 8, cells: []excelCell{{1, "Finished", 3, false}, {2, excelDateValue(document.FinishedAt), 10, true}}},
		excelRow{index: 9, cells: []excelCell{{1, "Duration", 3, false}, {2, excelDurationValue(document.FinishedAt.Sub(document.StartedAt)), 11, true}}},
		excelRow{index: 11, height: 22, cells: []excelCell{{1, "Summary metrics", 2, false}}},
	)
	sheet.merges = append(sheet.merges, "A11:B11")
	row := 12
	for _, item := range document.Summary {
		value, numeric := excelSummaryValue(item.Value)
		style := 4
		if numeric {
			style = 12
		}
		sheet.rows = append(sheet.rows, excelRow{index: row, cells: []excelCell{
			{1, item.Label, 3, false}, {2, value, style, numeric},
		}})
		row++
	}
	row++
	tableHeader := row
	sheet.rows = append(sheet.rows, excelRow{index: row, height: 24, cells: []excelCell{
		{1, "Sheet", 5, false}, {2, "Feature / Step", 5, false}, {3, "Target", 5, false},
		{4, "Status", 5, false}, {5, "Checks", 5, false}, {6, "Pass", 5, false},
		{7, "Fail", 5, false}, {8, "Skip", 5, false}, {9, "Warn", 5, false},
		{10, "Duration", 5, false}, {11, "Message", 5, false},
	}})
	row++
	for index, entry := range document.Entries {
		checklist := parseChecklistOutput(entry.Output)
		total := checklist.Counts["PASS"] + checklist.Counts["FAIL"] + checklist.Counts["SKIP"] + checklist.Counts["WARN"]
		sheet.rows = append(sheet.rows, excelRow{index: row, cells: []excelCell{
			{1, entrySheets[index], 4, false}, {2, entry.Title, 4, false}, {3, valueOrDash(entry.Target), 4, false},
			{4, normalizeChecklistStatus(entry.Status), excelStatusStyle(entry.Status), false},
			{5, strconv.Itoa(total), 12, true}, {6, strconv.Itoa(checklist.Counts["PASS"]), 12, true},
			{7, strconv.Itoa(checklist.Counts["FAIL"]), 12, true}, {8, strconv.Itoa(checklist.Counts["SKIP"]), 12, true},
			{9, strconv.Itoa(checklist.Counts["WARN"]), 12, true}, {10, excelDurationValue(entry.Duration), 11, true},
			{11, excelCellText(entry.Message), 4, false},
		}})
		row++
	}
	if len(document.Entries) > 0 {
		sheet.autoFilter = fmt.Sprintf("A%d:K%d", tableHeader, row-1)
	}
	return sheet
}

func buildExcelEntry(entry Entry, name string) excelSheet {
	checklist := parseChecklistOutput(entry.Output)
	sheet := excelSheet{
		name: name, columns: []float64{12, 38, 13, 24, 24, 20, 56}, freezeRows: 9,
		merges: []string{"A1:G1", "B4:G4", "A6:G6"},
	}
	total := checklist.Counts["PASS"] + checklist.Counts["FAIL"] + checklist.Counts["SKIP"] + checklist.Counts["WARN"]
	sheet.rows = append(sheet.rows,
		excelRow{index: 1, height: 28, cells: []excelCell{{1, entry.Title, 1, false}}},
		excelRow{index: 3, cells: []excelCell{
			{1, "Status", 3, false}, {2, normalizeChecklistStatus(entry.Status), excelStatusStyle(entry.Status), false},
			{3, "Target", 3, false}, {4, valueOrDash(entry.Target), 4, false},
			{5, "Duration", 3, false}, {6, excelDurationValue(entry.Duration), 11, true},
		}},
		excelRow{index: 4, cells: []excelCell{{1, "Message", 3, false}, {2, excelCellText(entry.Message), 4, false}}},
		excelRow{index: 6, height: 22, cells: []excelCell{{1, "Checklist summary", 2, false}}},
		excelRow{index: 7, cells: []excelCell{
			{1, "Checks", 3, false}, {2, strconv.Itoa(total), 4, false},
			{3, "Pass", 3, false}, {4, strconv.Itoa(checklist.Counts["PASS"]), 6, false},
			{5, "Fail", 3, false}, {6, strconv.Itoa(checklist.Counts["FAIL"]), 7, false},
		}},
		excelRow{index: 8, cells: []excelCell{
			{1, "Skip", 3, false}, {2, strconv.Itoa(checklist.Counts["SKIP"]), 8, false},
			{3, "Warn", 3, false}, {4, strconv.Itoa(checklist.Counts["WARN"]), 15, false},
			{5, "Messages", 3, false}, {6, strconv.Itoa(len(checklist.Messages)), 4, false},
		}},
	)

	row := 10
	for _, section := range checklist.Sections {
		if row+2 >= excelMaxRows {
			break
		}
		sectionCounts := countChecklistItems(section.Items)
		sheet.rows = append(sheet.rows, excelRow{index: row, height: 22, cells: []excelCell{
			{1, section.Title, 13, false}, {4, "PASS: " + strconv.Itoa(sectionCounts["PASS"]), 6, false},
			{5, "FAIL: " + strconv.Itoa(sectionCounts["FAIL"]), 7, false},
			{6, "SKIP: " + strconv.Itoa(sectionCounts["SKIP"]), 8, false},
			{7, "WARN: " + strconv.Itoa(sectionCounts["WARN"]), 15, false},
		}})
		sheet.merges = append(sheet.merges, fmt.Sprintf("A%d:C%d", row, row))
		row++
		sheet.rows = append(sheet.rows, excelRow{index: row, height: 24, cells: checklistTableHeader()})
		row++
		for index, item := range section.Items {
			if row >= excelMaxRows {
				break
			}
			comparisonStyle := 4
			if item.Status == "FAIL" && (item.Expected != "" || item.Actual != "") {
				comparisonStyle = 16
			}
			sheet.rows = append(sheet.rows, excelRow{index: row, cells: []excelCell{
				{1, strconv.Itoa(index + 1), 14, true}, {2, excelCellText(item.Check), 4, false},
				{3, item.Status, excelStatusStyle(item.Status), false},
				{4, excelCellText(item.Expected), comparisonStyle, false}, {5, excelCellText(item.Actual), comparisonStyle, false},
				{6, excelCellText(item.Source), 4, false}, {7, excelCellText(item.Details), 4, false},
			}})
			row++
		}
		row++
	}
	if len(checklist.Messages) > 0 && row+1 < excelMaxRows {
		sheet.rows = append(sheet.rows, excelRow{index: row, height: 22, cells: []excelCell{{1, "Additional messages", 13, false}}})
		sheet.merges = append(sheet.merges, fmt.Sprintf("A%d:G%d", row, row))
		row++
		for _, message := range checklist.Messages {
			if row >= excelMaxRows {
				break
			}
			sheet.rows = append(sheet.rows, excelRow{index: row, cells: []excelCell{{1, excelCellText(message), 4, false}}})
			sheet.merges = append(sheet.merges, fmt.Sprintf("A%d:G%d", row, row))
			row++
		}
	}
	if len(checklist.Sections) == 0 && len(checklist.Messages) == 0 {
		sheet.rows = append(sheet.rows, excelRow{index: row, cells: []excelCell{{1, "No checklist output", 4, false}}})
		sheet.merges = append(sheet.merges, fmt.Sprintf("A%d:G%d", row, row))
	}
	return sheet
}

func checklistTableHeader() []excelCell {
	return []excelCell{
		{1, "#", 5, false}, {2, "Check", 5, false}, {3, "Status", 5, false},
		{4, "Expected", 5, false}, {5, "Actual", 5, false}, {6, "Source", 5, false},
		{7, "Details", 5, false},
	}
}

func countChecklistItems(items []checklistItem) map[string]int {
	counts := map[string]int{"PASS": 0, "FAIL": 0, "SKIP": 0, "WARN": 0}
	for _, item := range items {
		counts[item.Status]++
	}
	return counts
}

func excelSheetNames(entries []Entry) []string {
	used := map[string]bool{"summary": true}
	names := make([]string, len(entries))
	for index, entry := range entries {
		base := entry.Title
		if separator := strings.LastIndex(base, " - "); separator >= 0 {
			base = base[separator+3:]
		}
		base = strings.Map(func(character rune) rune {
			if strings.ContainsRune("[]:*?/\\", character) || character < 0x20 {
				return '-'
			}
			return character
		}, strings.TrimSpace(base))
		base = strings.Trim(base, " '")
		if base == "" {
			base = "feature"
		}
		prefix := fmt.Sprintf("%02d-", index+1)
		candidate := truncateRunes(prefix+base, 31)
		for suffix := 2; used[strings.ToLower(candidate)]; suffix++ {
			tail := fmt.Sprintf("-%d", suffix)
			candidate = truncateRunes(prefix+base, 31-utf8.RuneCountInString(tail)) + tail
		}
		used[strings.ToLower(candidate)] = true
		names[index] = candidate
	}
	return names
}

func excelSummaryValue(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if _, err := strconv.ParseFloat(trimmed, 64); err == nil && trimmed != "" {
		return trimmed, true
	}
	return excelCellText(value), false
}

func excelCellText(value string) string {
	value = cleanOutput(value)
	return truncateRunes(value, excelMaxCellRunes)
}

func truncateRunes(value string, maximum int) string {
	if utf8.RuneCountInString(value) <= maximum {
		return value
	}
	runes := []rune(value)
	if maximum <= 1 {
		return string(runes[:maximum])
	}
	return string(runes[:maximum-1]) + "…"
}

func excelDateValue(value time.Time) string {
	if value.IsZero() {
		return "0"
	}
	local := time.Date(value.Year(), value.Month(), value.Day(), value.Hour(), value.Minute(), value.Second(), value.Nanosecond(), time.UTC)
	base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	return strconv.FormatFloat(local.Sub(base).Hours()/24, 'f', 10, 64)
}

func excelDurationValue(value time.Duration) string {
	if value < 0 {
		value = 0
	}
	return strconv.FormatFloat(value.Hours()/24, 'f', 10, 64)
}

func excelStatusStyle(status string) int {
	switch normalizeChecklistStatus(status) {
	case "PASS", "DONE":
		return 6
	case "FAIL", "FAILED", "CANCELLED":
		return 7
	case "SKIP", "SKIPPED", "PLANNED":
		return 8
	case "WARN", "PARTIAL":
		return 15
	default:
		return 4
	}
}

func excelWorksheet(sheet excelSheet) string {
	var output strings.Builder
	output.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	output.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	lastRow, lastColumn := 1, 1
	for _, row := range sheet.rows {
		if row.index > lastRow {
			lastRow = row.index
		}
		for _, cell := range row.cells {
			if cell.column > lastColumn {
				lastColumn = cell.column
			}
		}
	}
	fmt.Fprintf(&output, `<dimension ref="A1:%s%d"/>`, excelColumnName(lastColumn), lastRow)
	output.WriteString(`<sheetViews><sheetView workbookViewId="0" showGridLines="0">`)
	if sheet.freezeRows > 0 {
		fmt.Fprintf(&output, `<pane ySplit="%d" topLeftCell="A%d" activePane="bottomLeft" state="frozen"/>`, sheet.freezeRows, sheet.freezeRows+1)
	}
	output.WriteString(`</sheetView></sheetViews><sheetFormatPr defaultRowHeight="15"/>`)
	if len(sheet.columns) > 0 {
		output.WriteString(`<cols>`)
		for index, width := range sheet.columns {
			fmt.Fprintf(&output, `<col min="%d" max="%d" width="%.1f" customWidth="1"/>`, index+1, index+1, width)
		}
		output.WriteString(`</cols>`)
	}
	output.WriteString(`<sheetData>`)
	for _, row := range sheet.rows {
		fmt.Fprintf(&output, `<row r="%d"`, row.index)
		if row.height > 0 {
			fmt.Fprintf(&output, ` ht="%.1f" customHeight="1"`, row.height)
		}
		output.WriteString(`>`)
		for _, cell := range row.cells {
			reference := excelColumnName(cell.column) + strconv.Itoa(row.index)
			if cell.number {
				fmt.Fprintf(&output, `<c r="%s" s="%d"><v>%s</v></c>`, reference, cell.style, cell.value)
			} else {
				fmt.Fprintf(&output, `<c r="%s" s="%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, reference, cell.style, xmlEscape(cell.value))
			}
		}
		output.WriteString(`</row>`)
	}
	output.WriteString(`</sheetData>`)
	if sheet.autoFilter != "" {
		fmt.Fprintf(&output, `<autoFilter ref="%s"/>`, sheet.autoFilter)
	}
	if len(sheet.merges) > 0 {
		fmt.Fprintf(&output, `<mergeCells count="%d">`, len(sheet.merges))
		for _, reference := range sheet.merges {
			fmt.Fprintf(&output, `<mergeCell ref="%s"/>`, reference)
		}
		output.WriteString(`</mergeCells>`)
	}
	output.WriteString(`<pageMargins left="0.3" right="0.3" top="0.5" bottom="0.5" header="0.2" footer="0.2"/>`)
	output.WriteString(`</worksheet>`)
	return output.String()
}

func excelWorkbook(sheets []excelSheet) string {
	var output strings.Builder
	output.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	output.WriteString(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	for index, sheet := range sheets {
		fmt.Fprintf(&output, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, xmlEscape(sheet.name), index+1, index+1)
	}
	output.WriteString(`</sheets><calcPr calcId="191029" fullCalcOnLoad="1"/></workbook>`)
	return output.String()
}

func excelWorkbookRelationships(sheetCount int) string {
	var output strings.Builder
	output.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	output.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for index := 1; index <= sheetCount; index++ {
		fmt.Fprintf(&output, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, index, index)
	}
	fmt.Fprintf(&output, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`, sheetCount+1)
	output.WriteString(`</Relationships>`)
	return output.String()
}

func excelContentTypes(sheetCount int) string {
	var output strings.Builder
	output.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	output.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)
	for index := 1; index <= sheetCount; index++ {
		fmt.Fprintf(&output, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, index)
	}
	output.WriteString(`</Types>`)
	return output.String()
}

func excelColumnName(column int) string {
	var result string
	for column > 0 {
		column--
		result = string(rune('A'+column%26)) + result
		column /= 26
	}
	return result
}

func xmlEscape(value string) string {
	var output strings.Builder
	value = strings.Map(func(character rune) rune {
		if character == '\t' || character == '\n' || character == '\r' || character >= 0x20 {
			return character
		}
		return '�'
	}, value)
	_ = xml.EscapeText(&output, []byte(value))
	return output.String()
}

const excelRootRelationships = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`

const excelStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
<numFmts count="2"><numFmt numFmtId="164" formatCode="yyyy-mm-dd hh:mm:ss"/><numFmt numFmtId="165" formatCode="[h]:mm:ss.000"/></numFmts>
<fonts count="4"><font><sz val="11"/><name val="Calibri"/></font><font><b/><color rgb="FFFFFFFF"/><sz val="16"/><name val="Calibri"/></font><font><b/><color rgb="FFFFFFFF"/><sz val="11"/><name val="Calibri"/></font><font><b/><color rgb="FF17324D"/><sz val="11"/><name val="Calibri"/></font></fonts>
<fills count="9"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FF103B69"/><bgColor indexed="64"/></patternFill></fill><fill><patternFill patternType="solid"><fgColor rgb="FF08758B"/><bgColor indexed="64"/></patternFill></fill><fill><patternFill patternType="solid"><fgColor rgb="FFE8F0F8"/><bgColor indexed="64"/></patternFill></fill><fill><patternFill patternType="solid"><fgColor rgb="FFDDF3E4"/><bgColor indexed="64"/></patternFill></fill><fill><patternFill patternType="solid"><fgColor rgb="FFFCE8E6"/><bgColor indexed="64"/></patternFill></fill><fill><patternFill patternType="solid"><fgColor rgb="FFFFF1C2"/><bgColor indexed="64"/></patternFill></fill><fill><patternFill patternType="solid"><fgColor rgb="FFFFE0B2"/><bgColor indexed="64"/></patternFill></fill></fills>
<borders count="2"><border><left/><right/><top/><bottom/><diagonal/></border><border><left/><right/><top/><bottom style="thin"><color rgb="FFD5DEE8"/></bottom><diagonal/></border></borders>
<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>
<cellXfs count="17"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="1" fillId="2" borderId="0" xfId="0" applyFont="1" applyFill="1" applyAlignment="1"><alignment vertical="center"/></xf><xf numFmtId="0" fontId="2" fillId="3" borderId="0" xfId="0" applyFont="1" applyFill="1"/><xf numFmtId="0" fontId="3" fillId="4" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1"/><xf numFmtId="0" fontId="0" fillId="0" borderId="1" xfId="0" applyBorder="1" applyAlignment="1"><alignment vertical="top" wrapText="1"/></xf><xf numFmtId="0" fontId="2" fillId="2" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1"/><xf numFmtId="0" fontId="3" fillId="5" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1"/><xf numFmtId="0" fontId="3" fillId="6" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1"/><xf numFmtId="0" fontId="3" fillId="7" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1"/><xf numFmtId="0" fontId="0" fillId="0" borderId="1" xfId="0" applyBorder="1" applyAlignment="1"><alignment horizontal="right"/></xf><xf numFmtId="164" fontId="0" fillId="0" borderId="1" xfId="0" applyNumberFormat="1" applyBorder="1"/><xf numFmtId="165" fontId="0" fillId="0" borderId="1" xfId="0" applyNumberFormat="1" applyBorder="1"/><xf numFmtId="1" fontId="0" fillId="0" borderId="1" xfId="0" applyNumberFormat="1" applyBorder="1"/><xf numFmtId="0" fontId="3" fillId="4" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1" applyAlignment="1"><alignment vertical="center"/></xf><xf numFmtId="1" fontId="0" fillId="0" borderId="1" xfId="0" applyNumberFormat="1" applyBorder="1" applyAlignment="1"><alignment horizontal="center"/></xf><xf numFmtId="0" fontId="3" fillId="8" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1"/><xf numFmtId="0" fontId="0" fillId="6" borderId="1" xfId="0" applyFill="1" applyBorder="1" applyAlignment="1"><alignment vertical="top" wrapText="1"/></xf></cellXfs>
<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles><dxfs count="0"/><tableStyles count="0" defaultTableStyle="TableStyleMedium2" defaultPivotStyle="PivotStyleLight16"/>
</styleSheet>`
