package main

import (
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// Config holds CLI configuration flags
type Config struct {
	File1         string
	File2         string
	Sheet1        string
	Sheet2        string
	OutputFile    string
	CaseSensitive bool
	StrictDate    bool
	NormalizeList bool
}

// DiffRecord represents a single discrepancy or missing case for the output excel
type DiffRecord struct {
	CaseID string
	Field  string
	Val1   string
	Val2   string
	Status string
}

func main() {
	// Define CLI flags
	file1 := flag.String("f1", "", "Path to the first Excel file (required)")
	file2 := flag.String("f2", "", "Path to the second Excel file (required)")
	sheet1 := flag.String("s1", "Sheet1", "Sheet name for file 1")
	sheet2 := flag.String("s2", "Sheet1", "Sheet name for file 2")
	output := flag.String("output", "comparison_report.xlsx", "Path for the output Excel report")
	caseSensitive := flag.Bool("case-sensitive", false, "Enable case-sensitive comparison (true/false)")
	strictDate := flag.Bool("strict-date", false, "Enable strict date format comparison (true/false)")
	normalizeList := flag.Bool("normalize-list", true, "Treat comma and semicolon lists (e.g., 'a, b' vs 'a; b') as equal (true/false)")
	flag.Parse()

	if *file1 == "" || *file2 == "" {
		log.Fatal("Error: Both -f1 and -f2 file paths are required. Use -h for help.")
	}

	cfg := Config{
		File1:         *file1,
		File2:         *file2,
		Sheet1:        *sheet1,
		Sheet2:        *sheet2,
		OutputFile:    *output,
		CaseSensitive: *caseSensitive,
		StrictDate:    *strictDate,
		NormalizeList: *normalizeList,
	}

	fmt.Printf("Comparing:\n  File 1: %s [%s]\n  File 2: %s [%s]\n\n", cfg.File1, cfg.Sheet1, cfg.File2, cfg.Sheet2)

	// Load Data from Excel sheets
	headers1, data1, err := loadExcelData(cfg.File1, cfg.Sheet1)
	if err != nil {
		log.Fatalf("Failed to read File 1: %v", err)
	}

	_, data2, err := loadExcelData(cfg.File2, cfg.Sheet2)
	if err != nil {
		log.Fatalf("Failed to read File 2: %v", err)
	}

	// Run Analysis and collect discrepancy records
	records := compareData(headers1, data1, data2, cfg)

	// Export results to Excel
	if err := writeReportToExcel(cfg.OutputFile, records); err != nil {
		log.Fatalf("Failed to write output report Excel: %v", err)
	}

	fmt.Printf("\n[Success] Analysis report successfully generated: %s\n", cfg.OutputFile)
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
func compareData(headers []string, data1, data2 map[string]map[string]string, cfg Config) []DiffRecord {
	var records []DiffRecord

	fmt.Println("================== ANALYSIS REPORT ==================")

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

		for _, header := range headers {
			val1 := row1[header]
			val2 := row2[header]

			if !compareValues(val1, val2, header, cfg) {
				caseHasDiff = true
				records = append(records, DiffRecord{
					CaseID: caseID,
					Field:  header,
					Val1:   val1,
					Val2:   val2,
					Status: "Mismatch",
				})
				fmt.Printf("    * Field [%s] mismatch for Case ID %s: Sheet1='%s' vs Sheet2='%s'\n", header, caseID, val1, val2)
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

	return records
}

// compareValues compares cell values considering case-sensitivity, date formatting, and list normalization
func compareValues(val1, val2, header string, cfg Config) bool {
	// 0. Raw direct match check
	if val1 == val2 {
		return true
	}

	// 1. Case Sensitivity Check
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
		"02 Jan 2006",        // Supports format like "06 Aug 2025"
		"02 Jan 2006 15:04:05", // Supports format like "06 Aug 2025 14:30:00"
		"02-Jan-2006",        // Supports format like "10-Mar-2027"
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

// writeReportToExcel creates the final Excel sheet with CaseID, Field, Value1, Value2, and Status
func writeReportToExcel(outputPath string, records []DiffRecord) error {
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

	for rowIdx, rec := range records {
		rNum := rowIdx + 2
		f.SetCellValue(sheetName, fmt.Sprintf("A%d", rNum), rec.CaseID)
		f.SetCellValue(sheetName, fmt.Sprintf("B%d", rNum), rec.Field)
		f.SetCellValue(sheetName, fmt.Sprintf("C%d", rNum), rec.Val1)
		f.SetCellValue(sheetName, fmt.Sprintf("D%d", rNum), rec.Val2)
		f.SetCellValue(sheetName, fmt.Sprintf("E%d", rNum), rec.Status)
	}

	if err := f.SaveAs(outputPath); err != nil {
		return err
	}

	return nil
}
