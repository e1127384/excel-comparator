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
	CaseQualifier string   `yaml:"caseQualifier"`
}

// ------------------------------------------------------------
// Qualifier DSL – token types
// ------------------------------------------------------------

const (
	tokWord   = "WORD"
	tokEQ     = "EQ"
	tokNEQ    = "NEQ"
	tokAnd    = "AND"
	tokOr     = "OR"
	tokNot    = "NOT"
	tokIn     = "IN"
	tokLParen = "LPAREN"
	tokRParen = "RPAREN"
	tokComma  = "COMMA"
	tokEOF    = "EOF"
)

type qualToken struct {
	typ string
	val string
}

// tokenizeQualifier converts a qualifier expression string into a list of tokens.
func tokenizeQualifier(expr string) []qualToken {
	var tokens []qualToken
	i := 0
	for i < len(expr) {
		ch := expr[i]
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			i++
			continue
		}
		if ch == '(' {
			tokens = append(tokens, qualToken{tokLParen, "("})
			i++
			continue
		}
		if ch == ')' {
			tokens = append(tokens, qualToken{tokRParen, ")"})
			i++
			continue
		}
		if ch == ',' {
			tokens = append(tokens, qualToken{tokComma, ","})
			i++
			continue
		}
		if ch == '!' && i+1 < len(expr) && expr[i+1] == '=' {
			tokens = append(tokens, qualToken{tokNEQ, "!="})
			i += 2
			continue
		}
		if ch == '=' {
			tokens = append(tokens, qualToken{tokEQ, "="})
			i++
			continue
		}
		// Quoted string (single or double quotes)
		if ch == '"' || ch == '\'' {
			quote := ch
			i++
			start := i
			for i < len(expr) && expr[i] != quote {
				i++
			}
			tokens = append(tokens, qualToken{tokWord, expr[start:i]})
			if i < len(expr) {
				i++ // skip closing quote
			}
			continue
		}
		// Bare word / keyword
		start := i
		for i < len(expr) && expr[i] != ' ' && expr[i] != '\t' && expr[i] != '\n' && expr[i] != '\r' &&
			expr[i] != '(' && expr[i] != ')' && expr[i] != ',' && expr[i] != '=' && expr[i] != '!' {
			i++
		}
		word := expr[start:i]
		switch strings.ToUpper(word) {
		case "AND":
			tokens = append(tokens, qualToken{tokAnd, word})
		case "OR":
			tokens = append(tokens, qualToken{tokOr, word})
		case "NOT":
			tokens = append(tokens, qualToken{tokNot, word})
		case "IN":
			tokens = append(tokens, qualToken{tokIn, word})
		default:
			tokens = append(tokens, qualToken{tokWord, word})
		}
	}
	tokens = append(tokens, qualToken{tokEOF, ""})
	return tokens
}

// ------------------------------------------------------------
// Qualifier DSL – expression tree and evaluator
// ------------------------------------------------------------

// QualExpr is a boolean expression that can evaluate a data row.
type QualExpr interface {
	Evaluate(row map[string]string, caseSensitive bool) bool
}

// QualCondExpr is a single field comparison condition.
type QualCondExpr struct {
	Field  string
	Op     string   // "=", "!=", "IN", "NOT IN"
	Value  string   // used by "=" and "!="
	Values []string // used by "IN" and "NOT IN"
}

// QualAndExpr is a logical AND of two expressions.
type QualAndExpr struct{ Left, Right QualExpr }

// QualOrExpr is a logical OR of two expressions.
type QualOrExpr struct{ Left, Right QualExpr }

func (e *QualCondExpr) Evaluate(row map[string]string, caseSensitive bool) bool {
	// Resolve field value; try exact then case-insensitive header match.
	fieldVal, ok := row[e.Field]
	if !ok {
		for k, v := range row {
			if strings.EqualFold(k, e.Field) {
				fieldVal = v
				ok = true
				break
			}
		}
	}
	if !ok {
		return false
	}

	eq := func(a, b string) bool {
		if caseSensitive {
			return a == b
		}
		return strings.EqualFold(a, b)
	}

	switch e.Op {
	case "=":
		return eq(fieldVal, e.Value)
	case "!=":
		return !eq(fieldVal, e.Value)
	case "IN":
		for _, v := range e.Values {
			if eq(fieldVal, v) {
				return true
			}
		}
		return false
	case "NOT IN":
		for _, v := range e.Values {
			if eq(fieldVal, v) {
				return false
			}
		}
		return true
	}
	return false
}

func (e *QualAndExpr) Evaluate(row map[string]string, caseSensitive bool) bool {
	return e.Left.Evaluate(row, caseSensitive) && e.Right.Evaluate(row, caseSensitive)
}

func (e *QualOrExpr) Evaluate(row map[string]string, caseSensitive bool) bool {
	return e.Left.Evaluate(row, caseSensitive) || e.Right.Evaluate(row, caseSensitive)
}

// ------------------------------------------------------------
// Qualifier DSL – recursive-descent parser
// ------------------------------------------------------------

type qualParser struct {
	tokens []qualToken
	pos    int
}

func (p *qualParser) peek() qualToken {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return qualToken{tokEOF, ""}
}

func (p *qualParser) consume() qualToken {
	t := p.peek()
	p.pos++
	return t
}

// parseQualifierExpr is the entry point that tokenises and parses the expression.
func parseQualifierExpr(expr string) (QualExpr, error) {
	tokens := tokenizeQualifier(expr)
	p := &qualParser{tokens: tokens}
	e, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().typ != tokEOF {
		return nil, fmt.Errorf("unexpected token %q in qualifier", p.peek().val)
	}
	return e, nil
}

func (p *qualParser) parseOr() (QualExpr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().typ == tokOr {
		p.consume()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &QualOrExpr{Left: left, Right: right}
	}
	return left, nil
}

func (p *qualParser) parseAnd() (QualExpr, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for p.peek().typ == tokAnd {
		p.consume()
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		left = &QualAndExpr{Left: left, Right: right}
	}
	return left, nil
}

func (p *qualParser) parsePrimary() (QualExpr, error) {
	if p.peek().typ == tokLParen {
		p.consume() // consume '('
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().typ != tokRParen {
			return nil, fmt.Errorf("expected ')' in qualifier, got %q", p.peek().val)
		}
		p.consume()
		return expr, nil
	}
	return p.parseCondition()
}

// parseCondition reads a single "<field> <op> <value>" or "<field> [NOT] IN (<list>)".
// Field names may be multi-word; parsing stops once an operator token is seen.
func (p *qualParser) parseCondition() (QualExpr, error) {
	// Accumulate WORDs as the field name until an operator token appears.
	var fieldParts []string
	for {
		t := p.peek()
		if t.typ != tokWord {
			break
		}
		p.consume()
		fieldParts = append(fieldParts, t.val)
		next := p.peek()
		if next.typ == tokEQ || next.typ == tokNEQ || next.typ == tokIn || next.typ == tokNot {
			break
		}
	}
	if len(fieldParts) == 0 {
		return nil, fmt.Errorf("expected field name in qualifier, got %q", p.peek().val)
	}
	field := strings.Join(fieldParts, " ")

	switch p.peek().typ {
	case tokEQ:
		p.consume()
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		return &QualCondExpr{Field: field, Op: "=", Value: val}, nil
	case tokNEQ:
		p.consume()
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		return &QualCondExpr{Field: field, Op: "!=", Value: val}, nil
	case tokIn:
		p.consume()
		vals, err := p.parseList()
		if err != nil {
			return nil, err
		}
		return &QualCondExpr{Field: field, Op: "IN", Values: vals}, nil
	case tokNot:
		p.consume()
		if p.peek().typ != tokIn {
			return nil, fmt.Errorf("expected IN after NOT in qualifier, got %q", p.peek().val)
		}
		p.consume()
		vals, err := p.parseList()
		if err != nil {
			return nil, err
		}
		return &QualCondExpr{Field: field, Op: "NOT IN", Values: vals}, nil
	default:
		return nil, fmt.Errorf("expected operator after field %q in qualifier, got %q", field, p.peek().val)
	}
}

// parseValue reads a (possibly multi-word) scalar value, stopping at AND / OR / ')' / EOF.
func (p *qualParser) parseValue() (string, error) {
	var parts []string
	for {
		t := p.peek()
		if t.typ != tokWord {
			break
		}
		p.consume()
		parts = append(parts, t.val)
		next := p.peek()
		if next.typ == tokAnd || next.typ == tokOr || next.typ == tokEOF || next.typ == tokRParen {
			break
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("expected value in qualifier, got %q", p.peek().val)
	}
	return strings.Join(parts, " "), nil
}

// parseList reads a parenthesised, comma-separated list of values: (val1, val2, ...).
func (p *qualParser) parseList() ([]string, error) {
	if p.peek().typ != tokLParen {
		return nil, fmt.Errorf("expected '(' for IN list in qualifier, got %q", p.peek().val)
	}
	p.consume()
	var vals []string
	for {
		val, err := p.parseListValue()
		if err != nil {
			return nil, err
		}
		vals = append(vals, val)
		if p.peek().typ == tokComma {
			p.consume()
			continue
		}
		break
	}
	if p.peek().typ != tokRParen {
		return nil, fmt.Errorf("expected ')' to close IN list in qualifier, got %q", p.peek().val)
	}
	p.consume()
	return vals, nil
}

// parseListValue reads a single list element (multi-word, stops at comma or ')').
func (p *qualParser) parseListValue() (string, error) {
	var parts []string
	for {
		t := p.peek()
		if t.typ != tokWord {
			break
		}
		p.consume()
		parts = append(parts, t.val)
		next := p.peek()
		if next.typ == tokComma || next.typ == tokRParen || next.typ == tokEOF {
			break
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("expected value in IN list qualifier, got %q", p.peek().val)
	}
	return strings.Join(parts, " "), nil
}

// DiffRecord represents a single discrepancy or missing case for the output excel
type DiffRecord struct {
	CaseID string
	Field  string
	Val1   string
	Val2   string
	Status string
}

// MappingRule stores value translations: FieldName -> {OldVal -> NewVal}
type MappingRule map[string]map[string]string

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

	// Parse case qualifier if provided
	var qualExpr QualExpr
	if cfg.CaseQualifier != "" {
		qualExpr, err = parseQualifierExpr(cfg.CaseQualifier)
		if err != nil {
			log.Fatalf("Invalid caseQualifier: %v", err)
		}
	}

	fmt.Printf("Comparing:\n  File 1: %s [%s]\n  File 2: %s [%s]\n", cfg.File1, cfg.Sheet1, cfg.File2, cfg.Sheet2)
	if cfg.MappingFile != "" {
		fmt.Printf("  Mapping File: %s\n", cfg.MappingFile)
	}
	if cfg.CaseQualifier != "" {
		fmt.Printf("  Case Qualifier: %s\n", cfg.CaseQualifier)
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
	records := compareData(fieldsToCompare, data1, data2, mappingRules, cfg, qualExpr)

	// Export results to Excel
	if err := writeReportToExcel(cfg.OutputFile, records); err != nil {
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
func compareData(fieldsToCompare []string, data1, data2 map[string]map[string]string, mappingRules MappingRule, cfg Config, qualExpr QualExpr) []DiffRecord {
	var records []DiffRecord

	// Apply case qualifier to filter which File 1 records are considered
	filteredData1 := data1
	if qualExpr != nil {
		filteredData1 = make(map[string]map[string]string)
		for caseID, row := range data1 {
			if qualExpr.Evaluate(row, cfg.CaseSensitive) {
				filteredData1[caseID] = row
			}
		}
		fmt.Printf("Case Qualifier applied: %d of %d Sheet 1 records selected.\n\n", len(filteredData1), len(data1))
	}

	fmt.Println("================== ANALYSIS REPORT ==================")

	// 1. Check cases in File 1 but NOT in File 2
	fmt.Println("\n--- 1. Cases in Sheet 1 Missing from Sheet 2 ---")
	missingCount := 0
	for caseID := range filteredData1 {
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

	for caseID, row1 := range filteredData1 {
		row2, exists := data2[caseID]
		if !exists {
			continue
		}
		commonCount++
		caseHasDiff := false

		for _, header := range fieldsToCompare {
			val1 := row1[header]
			val2 := row2[header]

			// Apply migration mapping if available for this field
			mappedVal1 := applyMapping(header, val1, mappingRules)

			if !compareValues(mappedVal1, val2, header, cfg) {
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
	fmt.Printf("Total Cases in Sheet 1:   %d\n", len(data1))
	if qualExpr != nil {
		fmt.Printf("Cases After Qualifier:    %d\n", len(filteredData1))
	}
	fmt.Printf("Total Cases in Sheet 2:   %d\n", len(data2))
	fmt.Printf("Cases Missing in Sheet 2: %d\n", missingCount)
	fmt.Printf("Common Cases Evaluated:   %d\n", commonCount)
	fmt.Printf("Cases with Mismatches:    %d\n", diffCount)
	fmt.Println("=============================================")

	return records
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

// compareValues compares cell values considering case-sensitivity, SIS/GI equivalence, date formatting, and list normalization
func compareValues(val1, val2, header string, cfg Config) bool {
	// 0. Raw direct match check
	if val1 == val2 {
		return true
	}

	// 1. Case Sensitivity & SIS/GI Equivalence Preparation
	compVal1 := val1
	compVal2 := val2
	if !cfg.CaseSensitive {
		compVal1 = strings.ToLower(compVal1)
		compVal2 = strings.ToLower(compVal2)
	}

	if compVal1 == compVal2 {
		return true
	}

	// Treat SIS and GI as the same string
	if areSisAndGiEquivalent(compVal1, compVal2) {
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

// areSisAndGiEquivalent checks if one value is SIS and the other is GI
func areSisAndGiEquivalent(v1, v2 string) bool {
	normV1 := strings.ToUpper(strings.TrimSpace(v1))
	normV2 := strings.ToUpper(strings.TrimSpace(v2))
	return (normV1 == "SIS" && normV2 == "GI") || (normV1 == "GI" && normV2 == "SIS")
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
