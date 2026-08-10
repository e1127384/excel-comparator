package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
	"gopkg.in/yaml.v3"
)

// Config holds CLI configuration flags
type Config struct {
	File1         string   `yaml:"file1"`
	File2         string   `yaml:"file2"`
	Sheet1        string   `yaml:"sheet1"`
	Sheet2        string   `yaml:"sheet2"`
	MappingFile   string   `yaml:"mappingFile"`
	OutputFile    string   `yaml:"outputFile"`
	CaseSensitive bool     `yaml:"caseSensitive"`
	StrictDate    bool     `yaml:"strictDate"`
	NormalizeList bool     `yaml:"normalizeList"`
	CompareFields []string `yaml:"compareFields"`
}

// DiffRecord represents a single discrepancy or missing case for the output excel
type DiffRecord struct {
	CaseID string
	Field  string
	Val1   string
	Val2   string
	Status string
}

// FieldSummary captures field-wise population and mismatch metrics
type FieldSummary struct {
	Field             string
	PopulatedInSource int
	ComparedCount     int
	MismatchCount     int
}

// MappingRule stores value translations: FieldName -> {OldVal -> NewVal}
type MappingRule map[string]map[string]string

const (
	ragGreenThreshold = 0.05
	ragAmberThreshold = 0.20
)

func main() {
	// Define CLI flag for config file path
	configPath := flag.String("config", "config.yaml", "Path to configuration YAML file")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config file: %v", err)
	}

	if cfg.File1 == "" || cfg.File2 == "" {
		log.Fatal("Error: file1 and file2 are required in config.yaml")
	}

	fmt.Printf("Comparing:\n  File 1: %s [%s]\n  File 2: %s [%s]\n", cfg.File1, cfg.Sheet1, cfg.File2, cfg.Sheet2)
	if cfg.MappingFile != "" {
		fmt.Printf("  Mapping File: %s\n", cfg.MappingFile)
	}
	fmt.Println()

	// Load Migration Mapping Rules if provided
	var mappingRules MappingRule
	if cfg.MappingFile != "" {
		mappingRules, err = loadMappingRules(cfg.MappingFile)
		if err != nil {
			log.Fatalf("Failed to load mapping file: %v", err)
		}
	}

	// Load Data from Excel sheets
	headers1, data1, err := loadExcelData(cfg.File1, cfg.Sheet1)
	if err != nil {
		log.Fatalf("Failed to read File 1: %v", err)
	}

	_, data2, err := loadExcelData(cfg.File2, cfg.Sheet2)
	if err != nil {
		log.Fatalf("Failed to read File 2: %v", err)
	}

	fieldsToCompare, err := resolveFieldsToCompare(headers1, cfg.CompareFields)
	if err != nil {
		log.Fatalf("Failed to resolve compare fields: %v", err)
	}

	if len(cfg.CompareFields) > 0 {
		fmt.Printf("  Compare Fields: %s\n", strings.Join(fieldsToCompare, ", "))
	}

	// Run Analysis and collect discrepancy records
	records, fieldSummaries := compareData(fieldsToCompare, data1, data2, mappingRules, cfg)

	// Export results to Excel
	if err := writeReportToExcel(cfg.OutputFile, records, fieldSummaries); err != nil {
		log.Fatalf("Failed to write output report Excel: %v", err)
	}

	fmt.Printf("\n[Success] Analysis report successfully generated: %s\n", cfg.OutputFile)
}

// loadConfig reads configuration from a YAML file and applies defaults
func loadConfig(configPath string) (Config, error) {
	cfg := Config{
		Sheet1:        "Sheet1",
		Sheet2:        "Sheet1",
		OutputFile:    "comparison_report.xlsx",
		NormalizeList: true,
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return Config{}, err
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// resolveFieldsToCompare returns header names to compare, optionally filtered by configured fields
func resolveFieldsToCompare(headers []string, compareFields []string) ([]string, error) {
	if len(compareFields) == 0 {
		return headers, nil
	}

	headerMap := make(map[string]string, len(headers))
	for _, h := range headers {
		headerMap[strings.ToLower(strings.TrimSpace(h))] = h
	}

	fieldsToCompare := make([]string, 0, len(compareFields))
	seen := make(map[string]struct{}, len(compareFields))
	for _, field := range compareFields {
		normalized := strings.ToLower(strings.TrimSpace(field))
		header, ok := headerMap[normalized]
		if !ok {
			return nil, fmt.Errorf("field %q not found in Sheet 1 headers", field)
		}
		if _, exists := seen[header]; exists {
			continue
		}
		seen[header] = struct{}{}
		fieldsToCompare = append(fieldsToCompare, header)
	}

	return fieldsToCompare, nil
}

// loadMappingRules reads the migration Excel file containing FieldName, OldValue, NewValue
func loadMappingRules(filePath string) (MappingRule, error) {
	f, err := excelize.OpenFile(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	rows, err := f.GetRows(f.GetSheetName(0))
	if err != nil {
		return nil, err
	}

	rules := make(MappingRule)

	// Expecting headers: FieldName, OldValue, NewValue (starting from row index 1)
	for i := 1; i < len(rows); i++ {
		row := rows[i]
		if len(row) < 3 {
			continue
		}
		field := strings.TrimSpace(row[0])
		oldVal := strings.TrimSpace(row[1])
		newVal := strings.TrimSpace(row[2])

		if field == "" {
			continue
		}

		if rules[field] == nil {
			rules[field] = make(map[string]string)
		}
		rules[field][oldVal] = newVal
	}

	return rules, nil
}

// loadExcelData reads an excel sheet and maps CaseID (first column) -> Map[ColumnName]Value
func loadExcelData(filePath, sheetName string) ([]string, map[string]map[string]string, error) {
	f, err := excelize.OpenFile(filePath)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	rows, err := f.GetRows(sheetName)
	if err != nil {
		return nil, nil, err
	}

	if len(rows) == 0 {
		return nil, nil, fmt.Errorf("sheet %s is empty", sheetName)
	}

	headers := rows[0]
	data := make(map[string]map[string]string)

	for i := 1; i < len(rows); i++ {
		row := rows[i]
		if len(row) == 0 || row[0] == "" {
			continue // Skip blank rows
		}

		caseID := strings.TrimSpace(row[0])
		rowData := make(map[string]string)

		for j, header := range headers {
			val := ""
			if j < len(row) {
				val = strings.TrimSpace(row[j])
			}
			rowData[header] = val
		}
		data[caseID] = rowData
	}

	return headers, data, nil
}

// compareData performs the differential analysis and returns collected records
func compareData(fieldsToCompare []string, data1, data2 map[string]map[string]string, mappingRules MappingRule, cfg Config) ([]DiffRecord, []FieldSummary) {
	var records []DiffRecord
	fieldStats := make(map[string]FieldSummary, len(fieldsToCompare))

	fmt.Println("================== ANALYSIS REPORT ==================")

	for _, header := range fieldsToCompare {
		fieldStats[header] = FieldSummary{Field: header}
	}

	for _, row1 := range data1 {
		for _, header := range fieldsToCompare {
			if strings.TrimSpace(row1[header]) == "" {
				continue
			}
			stat := fieldStats[header]
			stat.PopulatedInSource++
			fieldStats[header] = stat
		}
	}

	// 1. Check cases in File 1 but NOT in File 2
	fmt.Println("\n--- 1. Cases in Sheet 1 Missing from Sheet 2 ---")
	missingCount := 0
	for caseID := range data1 {
		if _, exists := data2[caseID]; !exists {
			fmt.Printf("  [Missing] Case ID: %s\n", caseID)
			records = append(records, DiffRecord{
				CaseID: caseID,
				Field:  "[All Fields]",
				Val1:   "Present in Sheet 1",
				Val2:   "Missing in Sheet 2",
				Status: "Missing in Sheet 2",
			})
			missingCount++
		}
	}
	if missingCount == 0 {
		fmt.Println("  None. All cases from Sheet 1 are present in Sheet 2.")
	}

	// 2. Check cases in both files and compare field by field
	fmt.Println("\n--- 2. Field-by-Field Comparison (Common Cases) ---")
	commonCount := 0
	diffCount := 0

	for caseID, row1 := range data1 {
		row2, exists := data2[caseID]
		if !exists {
			continue
		}
		commonCount++
		caseHasDiff := false

		for _, header := range fieldsToCompare {
			val1 := row1[header]
			val2 := row2[header]
			populatedInSource := strings.TrimSpace(val1) != ""
			if populatedInSource {
				stat := fieldStats[header]
				stat.ComparedCount++
				fieldStats[header] = stat
			}

			// Apply migration mapping if available for this field
			mappedVal1 := applyMapping(header, val1, mappingRules)

			if !compareValues(mappedVal1, val2, header, cfg) {
				if populatedInSource {
					stat := fieldStats[header]
					stat.MismatchCount++
					fieldStats[header] = stat
				}
				caseHasDiff = true
				reportVal1 := val1
				if mappedVal1 != val1 {
					reportVal1 = fmt.Sprintf("%s (mapped to %s)", val1, mappedVal1)
				}
				records = append(records, DiffRecord{
					CaseID: caseID,
					Field:  header,
					Val1:   reportVal1,
					Val2:   val2,
					Status: "Mismatch",
				})
				fmt.Printf("    * Field [%s] mismatch for Case ID %s: Sheet1='%s' vs Sheet2='%s'\n", header, caseID, reportVal1, val2)
			}
		}

		if caseHasDiff {
			diffCount++
		}
	}

	fmt.Println("\n================== SUMMARY ==================")
	fmt.Printf("Total Cases in Sheet 1: %d\n", len(data1))
	fmt.Printf("Total Cases in Sheet 2: %d\n", len(data2))
	fmt.Printf("Cases Missing in Sheet 2: %d\n", missingCount)
	fmt.Printf("Common Cases Evaluated:   %d\n", commonCount)
	fmt.Printf("Cases with Mismatches:    %d\n", diffCount)
	fmt.Println("=============================================")

	summaries := make([]FieldSummary, 0, len(fieldsToCompare))
	for _, header := range fieldsToCompare {
		summaries = append(summaries, fieldStats[header])
	}

	return records, summaries
}

// applyMapping translates val1 based on the migration mapping rules if defined
func applyMapping(field, val1 string, rules MappingRule) string {
	if rules == nil {
		return val1
	}
	fieldRules, exists := rules[field]
	if !exists {
		return val1
	}
	if newVal, mapped := fieldRules[val1]; mapped {
		return newVal
	}
	return val1
}

// compareValues compares cell values considering case-sensitivity, date formatting, and list normalization
func compareValues(val1, val2, header string, cfg Config) bool {
	// 0. Raw direct match check
	if val1 == val2 {
		return true
	}

	// 1. Case Sensitivity Preparation
	compVal1 := val1
	compVal2 := val2
	if !cfg.CaseSensitive {
		compVal1 = strings.ToLower(compVal1)
		compVal2 = strings.ToLower(compVal2)
	}

	if compVal1 == compVal2 {
		return true
	}

	// 2. List Separator Normalization (e.g., ',' vs ';')
	if cfg.ConfigNormalizeList(compVal1, compVal2) {
		return true
	}

	// 3. Date Format Sensitivity Check (ignoring time component)
	if !cfg.StrictDate && isDateHeaderOrValue(header, val1, val2) {
		parsed1, err1 := parseDate(val1)
		parsed2, err2 := parseDate(val2)
		if err1 == nil && err2 == nil {
			if parsed1.Year() == parsed2.Year() && parsed1.Month() == parsed2.Month() && parsed1.Day() == parsed2.Day() {
				return true
			}
		}
	}

	return false
}

// ConfigNormalizeList normalizes commas and semicolons and compares them cleanly
func (cfg Config) ConfigNormalizeList(v1, v2 string) bool {
	if !cfg.NormalizeList {
		return false
	}
	norm1 := normalizeListString(v1)
	norm2 := normalizeListString(v2)
	return norm1 == norm2
}

// normalizeListString converts semicolons to commas and standardizes whitespace
func normalizeListString(s string) string {
	s = strings.ReplaceAll(s, ";", ",")
	parts := strings.Split(s, ",")
	var cleaned []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return strings.Join(cleaned, ", ")
}

// isDateHeaderOrValue checks if a field name or value indicates a date
func isDateHeaderOrValue(header string, vals ...string) bool {
	hLower := strings.ToLower(header)
	if strings.Contains(hLower, "date") || strings.Contains(hLower, "time") {
		return true
	}
	for _, v := range vals {
		if _, err := parseDate(v); err == nil {
			return true
		}
	}
	return false
}

// parseDate handles formats like "06 Aug 2025", "10-Mar-2027", standard ISO dates, and common slashes
func parseDate(val string) (time.Time, error) {
	formats := []string{
		"02 Jan 2006",
		"02 Jan 2006 15:04:05",
		"02-Jan-2006",
		"02-Jan-2006 15:04:05",
		"2006-01-02",
		"01/02/2006",
		"02-01-2006",
		"2006/01/02",
		"02/01/2006 15:04:05",
		"2006-01-02 15:04:05",
		time.RFC3339,
	}
	for _, layout := range formats {
		if t, err := time.Parse(layout, val); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("not a date")
}

// writeReportToExcel creates the final Excel sheet with detailed mismatches and field-wise summary
func writeReportToExcel(outputPath string, records []DiffRecord, summaries []FieldSummary) error {
	f := excelize.NewFile()
	sheetName := "Comparison Report"

	index, err := f.NewSheet(sheetName)
	if err != nil {
		return err
	}
	f.SetActiveSheet(index)
	f.DeleteSheet("Sheet1")

	headers := []string{"CaseID", "Field", "Sheet1 Value", "Sheet2 Value", "Status"}
	for colIdx, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(colIdx+1, 1)
		f.SetCellValue(sheetName, cell, header)
	}

	headerStyle, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{
			Type:    "pattern",
			Color:   []string{"#D9E1F2"},
			Pattern: 1,
		},
	})
	if err != nil {
		return err
	}
	if err := f.SetCellStyle(sheetName, "A1", "E1", headerStyle); err != nil {
		return err
	}

	redStyle, err := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{
			Type:    "pattern",
			Color:   []string{"#F8CBAD"},
			Pattern: 1,
		},
	})
	if err != nil {
		return err
	}
	amberStyle, err := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{
			Type:    "pattern",
			Color:   []string{"#FFF2CC"},
			Pattern: 1,
		},
	})
	if err != nil {
		return err
	}
	greenStyle, err := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{
			Type:    "pattern",
			Color:   []string{"#C6EFCE"},
			Pattern: 1,
		},
	})
	if err != nil {
		return err
	}

	for rowIdx, rec := range records {
		rNum := rowIdx + 2
		f.SetCellValue(sheetName, fmt.Sprintf("A%d", rNum), rec.CaseID)
		f.SetCellValue(sheetName, fmt.Sprintf("B%d", rNum), rec.Field)
		f.SetCellValue(sheetName, fmt.Sprintf("C%d", rNum), rec.Val1)
		f.SetCellValue(sheetName, fmt.Sprintf("D%d", rNum), rec.Val2)
		f.SetCellValue(sheetName, fmt.Sprintf("E%d", rNum), rec.Status)

		switch rec.Status {
		case "Mismatch":
			if err := f.SetCellStyle(sheetName, fmt.Sprintf("E%d", rNum), fmt.Sprintf("E%d", rNum), redStyle); err != nil {
				return err
			}
		case "Missing in Sheet 2":
			if err := f.SetCellStyle(sheetName, fmt.Sprintf("E%d", rNum), fmt.Sprintf("E%d", rNum), amberStyle); err != nil {
				return err
			}
		}
	}

	lastDetailRow := len(records) + 1 // +1 accounts for the report header row on row 1.
	summaryTitleRow := lastDetailRow + 3
	f.SetCellValue(sheetName, fmt.Sprintf("A%d", summaryTitleRow), "Field-wise Analysis")
	if err := f.SetCellStyle(sheetName, fmt.Sprintf("A%d", summaryTitleRow), fmt.Sprintf("A%d", summaryTitleRow), headerStyle); err != nil {
		return err
	}

	summaryHeaderRow := summaryTitleRow + 1
	summaryHeaders := []string{"Field", "Populated in Sheet1", "Populated Rows with Case in Sheet2", "Mismatch Count", "Mismatch %", "RAG Status"}
	for colIdx, header := range summaryHeaders {
		cell, _ := excelize.CoordinatesToCellName(colIdx+1, summaryHeaderRow)
		f.SetCellValue(sheetName, cell, header)
	}
	if err := f.SetCellStyle(sheetName, fmt.Sprintf("A%d", summaryHeaderRow), fmt.Sprintf("F%d", summaryHeaderRow), headerStyle); err != nil {
		return err
	}

	for i, summary := range summaries {
		rowNum := summaryHeaderRow + i + 1
		f.SetCellValue(sheetName, fmt.Sprintf("A%d", rowNum), summary.Field)
		f.SetCellValue(sheetName, fmt.Sprintf("B%d", rowNum), summary.PopulatedInSource)
		f.SetCellValue(sheetName, fmt.Sprintf("C%d", rowNum), summary.ComparedCount)
		f.SetCellValue(sheetName, fmt.Sprintf("D%d", rowNum), summary.MismatchCount)

		mismatchPercent := 0.0
		if summary.ComparedCount > 0 {
			mismatchPercent = float64(summary.MismatchCount) / float64(summary.ComparedCount) * 100
		}
		f.SetCellValue(sheetName, fmt.Sprintf("E%d", rowNum), fmt.Sprintf("%.2f%%", mismatchPercent))

		ragLabel, ragStyle := determineRAGStatus(summary.ComparedCount, summary.MismatchCount, redStyle, amberStyle, greenStyle)
		f.SetCellValue(sheetName, fmt.Sprintf("F%d", rowNum), ragLabel)
		if err := f.SetCellStyle(sheetName, fmt.Sprintf("F%d", rowNum), fmt.Sprintf("F%d", rowNum), ragStyle); err != nil {
			return err
		}
	}

	if err := f.SetColWidth(sheetName, "A", "A", 24); err != nil {
		return err
	}
	if err := f.SetColWidth(sheetName, "B", "F", 22); err != nil {
		return err
	}

	if err := f.SaveAs(outputPath); err != nil {
		return err
	}

	return nil
}

func determineRAGStatus(comparedCount, mismatchCount, redStyle, amberStyle, greenStyle int) (string, int) {
	if comparedCount == 0 {
		return "Amber (No comparable rows)", amberStyle
	}

	mismatchRate := float64(mismatchCount) / float64(comparedCount)
	if mismatchRate <= ragGreenThreshold {
		return "Green", greenStyle
	}
	if mismatchRate <= ragAmberThreshold {
		return "Amber", amberStyle
	}
	return "Red", redStyle
}
