package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
	"gopkg.in/yaml.v3"
)

// FieldGroup defines a named group of fields to compare as a logical unit
type FieldGroup struct {
	Name   string   `yaml:"name"`
	Fields []string `yaml:"fields"`
}

// Config holds CLI configuration flags
type Config struct {
	File1             string       `yaml:"file1"`
	File2             string       `yaml:"file2"`
	Sheet1            string       `yaml:"sheet1"`
	Sheet2            string       `yaml:"sheet2"`
	KeyFields         []string     `yaml:"keyFields"`
	MappingFile       string       `yaml:"mappingFile"`
	OutputFile        string       `yaml:"outputFile"`
	CaseSensitive     bool         `yaml:"caseSensitive"`
	StrictDate        bool         `yaml:"strictDate"`
	NormalizeList     bool         `yaml:"normalizeList"`
	CompareFields     []string     `yaml:"compareFields"`
	CaseQualifier     string       `yaml:"caseQualifier"`
	FieldGroups       []FieldGroup `yaml:"fieldGroups"`
	ShowMatchedValues bool         `yaml:"showMatchedValues"`
}

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
		if ch == '"' || ch == '\'' {
			quote := ch
			i++
			start := i
			for i < len(expr) && expr[i] != quote {
				i++
			}
			tokens = append(tokens, qualToken{tokWord, expr[start:i]})
			if i < len(expr) {
				i++
			}
			continue
		}
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

type QualExpr interface {
	Evaluate(row map[string]string, caseSensitive bool) bool
}

type QualCondExpr struct {
	Field  string
	Op     string
	Value  string
	Values []string
}

type QualAndExpr struct{ Left, Right QualExpr }
type QualOrExpr struct{ Left, Right QualExpr }

func (e *QualCondExpr) Evaluate(row map[string]string, caseSensitive bool) bool {
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
		p.consume()
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

func (p *qualParser) parseCondition() (QualExpr, error) {
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

// FieldSummary captures field-wise population and mismatch metrics
type FieldSummary struct {
	Field             string
	PopulatedInSource int
	ComparedCount     int
	MismatchCount     int
}

type GroupedDiffRecord struct {
	CaseID             string
	Field              string
	OldRawValue        string
	NewRawValue        string
	RawMatch           bool
	OldNormalizedValue string
	NewNormalizedValue string
	NormalizedMatch    bool
	FinalStatus        string
}

type GroupedSheetResult struct {
	SheetName string
	Records   []GroupedDiffRecord
}

type GroupedMappingWorkbook struct {
	SingleFieldRules MappingRule
	GroupRules       GroupMappingRule
	Groups           []FieldGroup
}

// MappingRule stores value translations: FieldName -> {OldVal -> NewVal}
type MappingRule map[string]map[string]string

const (
	ragGreenThreshold = 0.05
	ragAmberThreshold = 0.20
)

// GroupMappingRule stores group-level combination translations:
// GroupName -> {internalOldKey -> map[FieldName]NewVal}
// The internal key is built by joining old field values with "\x00" as separator.
type GroupMappingRule map[string]map[string]map[string]string

func main() {
	// Define CLI flag for config file path
	configPath := flag.String("config", "config.yaml", "Path to configuration YAML file")
	showMatchedValues := flag.Bool("show-matched-values", false, "Show old/new values for matched rows in grouped output mode, including SingleFields")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config file: %v", err)
	}

	if cfg.File1 == "" || cfg.File2 == "" {
		log.Fatal("Error: file1 and file2 are required in config.yaml")
	}
	if *showMatchedValues {
		cfg.ShowMatchedValues = true
	}

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
	var groupMappingRules GroupMappingRule
	var groupedWorkbook GroupedMappingWorkbook
	groupedMode := false
	if cfg.MappingFile != "" {
		groupedWorkbook, groupedMode, err = loadGroupedMappingWorkbook(cfg.MappingFile)
		if err != nil {
			log.Fatalf("Failed to load grouped mapping workbook: %v", err)
		}
		if groupedMode {
			mappingRules = groupedWorkbook.SingleFieldRules
			groupMappingRules = groupedWorkbook.GroupRules
		} else {
			mappingRules, err = loadMappingRules(cfg.MappingFile)
			if err != nil {
				log.Fatalf("Failed to load mapping file: %v", err)
			}
			groupMappingRules, err = loadGroupMappingRules(cfg.MappingFile, cfg.FieldGroups)
			if err != nil {
				log.Fatalf("Failed to load group mapping rules: %v", err)
			}
		}
	}

	// Load Data from Excel sheets
	headers1, data1, keyDisplay1, resolvedKeyFields, err := loadExcelData(cfg.File1, cfg.Sheet1, cfg.KeyFields)
	if err != nil {
		log.Fatalf("Failed to read File 1: %v", err)
	}

	_, data2, _, _, err := loadExcelData(cfg.File2, cfg.Sheet2, cfg.KeyFields)
	if err != nil {
		log.Fatalf("Failed to read File 2: %v", err)
	}

	fmt.Printf("  Key Fields: %s\n", strings.Join(resolvedKeyFields, ", "))

	configuredGroups := cfg.FieldGroups
	if groupedMode && len(groupedWorkbook.Groups) > 0 {
		configuredGroups = groupedWorkbook.Groups
	}
	fieldsToCompare, resolvedGroups, err := resolveFieldsToCompare(headers1, cfg.CompareFields, configuredGroups)
	if err != nil {
		log.Fatalf("Failed to resolve compare fields: %v", err)
	}

	if len(cfg.CompareFields) > 0 {
		fmt.Printf("  Compare Fields: %s\n", strings.Join(fieldsToCompare, ", "))
	}
	if len(resolvedGroups) > 0 {
		for _, g := range resolvedGroups {
			fmt.Printf("  Field Group [%s]: %s\n", g.Name, strings.Join(g.Fields, ", "))
		}
	}

	if groupedMode {
		groupResults := compareDataGrouped(fieldsToCompare, resolvedGroups, data1, data2, keyDisplay1, mappingRules, groupMappingRules, cfg, qualExpr)
		if err := writeGroupedReportToExcel(cfg.OutputFile, groupResults); err != nil {
			log.Fatalf("Failed to write grouped output report Excel: %v", err)
		}
	} else {
		// Run Analysis and collect discrepancy records
		records, fieldSummaries := compareData(fieldsToCompare, resolvedGroups, data1, data2, keyDisplay1, mappingRules, groupMappingRules, cfg, qualExpr)

		// Export results to Excel
		if err := writeReportToExcel(cfg.OutputFile, records, fieldSummaries); err != nil {
			log.Fatalf("Failed to write output report Excel: %v", err)
		}
	}

	fmt.Printf("\n[Success] Analysis report successfully generated: %s\n", cfg.OutputFile)
}

// loadConfig reads configuration from a YAML file and applies defaults
func loadConfig(configPath string) (Config, error) {
	cfg := Config{
		Sheet1:            "Sheet1",
		Sheet2:            "Sheet1",
		OutputFile:        "comparison_report.xlsx",
		NormalizeList:     true,
		ShowMatchedValues: false,
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

func loadGroupedMappingWorkbook(filePath string) (GroupedMappingWorkbook, bool, error) {
	f, err := excelize.OpenFile(filePath)
	if err != nil {
		return GroupedMappingWorkbook{}, false, err
	}
	defer f.Close()

	sheets := f.GetSheetList()
	hasSingleFields := false
	for _, s := range sheets {
		if strings.EqualFold(strings.TrimSpace(s), "SingleFields") {
			hasSingleFields = true
			break
		}
	}
	if !hasSingleFields {
		return GroupedMappingWorkbook{}, false, nil
	}

	wb := GroupedMappingWorkbook{
		SingleFieldRules: make(MappingRule),
		GroupRules:       make(GroupMappingRule),
		Groups:           make([]FieldGroup, 0),
	}

	for _, sheetName := range sheets {
		trimmedSheet := strings.TrimSpace(sheetName)
		if strings.EqualFold(trimmedSheet, "SingleFields") {
			parseSingleFieldMappingSheet(f, sheetName, wb.SingleFieldRules)
			continue
		}
		group, rules, ok := parseGroupedMappingSheet(f, sheetName)
		if !ok {
			continue
		}
		wb.Groups = append(wb.Groups, group)
		wb.GroupRules[group.Name] = rules
	}

	return wb, true, nil
}

func parseSingleFieldMappingSheet(f *excelize.File, sheetName string, rules MappingRule) {
	rows, err := f.GetRows(sheetName)
	if err != nil {
		log.Printf("Warning: unable to read sheet %q: %v", sheetName, err)
		return
	}
	if len(rows) == 0 {
		return
	}
	headerIdx := make(map[string]int)
	for i, h := range rows[0] {
		headerIdx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	fieldCol, okField := headerIdx["fieldname"]
	oldCol, okOld := headerIdx["oldvalue"]
	newCol, okNew := headerIdx["newvalue"]
	if !okField || !okOld {
		log.Printf("Warning: sheet %q missing required columns FieldName/OldValue; skipping", sheetName)
		return
	}
	if !okNew {
		log.Printf("Warning: sheet %q missing optional NewValue column; defaulting NewValue to OldValue", sheetName)
	}

	for i := 1; i < len(rows); i++ {
		row := rows[i]
		field := cellAt(row, fieldCol)
		oldVal := cellAt(row, oldCol)
		newVal := oldVal
		if okNew {
			newVal = cellAt(row, newCol)
		}
		if field == "" {
			if strings.TrimSpace(strings.Join(row, "")) != "" {
				log.Printf("Warning: sheet %q row %d has empty FieldName; skipping row", sheetName, i+1)
			}
			continue
		}
		if rules[field] == nil {
			rules[field] = make(map[string]string)
		}
		rules[field][oldVal] = newVal
	}
}

func parseGroupedMappingSheet(f *excelize.File, sheetName string) (FieldGroup, map[string]map[string]string, bool) {
	rows, err := f.GetRows(sheetName)
	if err != nil {
		log.Printf("Warning: unable to read group sheet %q: %v", sheetName, err)
		return FieldGroup{}, nil, false
	}
	if len(rows) < 2 {
		return FieldGroup{}, nil, false
	}

	headers := rows[0]
	fields, oldCols, newCols, ok := parseGroupHeaderColumns(headers)
	if !ok || len(fields) == 0 {
		log.Printf("Warning: group sheet %q has malformed header; expected old/new columns for each field", sheetName)
		return FieldGroup{}, nil, false
	}

	rules := make(map[string]map[string]string)
	for i := 1; i < len(rows); i++ {
		row := rows[i]
		oldVals := make([]string, len(fields))
		newVals := make(map[string]string, len(fields))
		for j, field := range fields {
			oldVals[j] = cellAt(row, oldCols[j])
			newVal := oldVals[j]
			if newCols[j] >= 0 {
				newVal = cellAt(row, newCols[j])
			}
			newVals[field] = newVal
		}
		allBlank := true
		for _, v := range oldVals {
			if strings.TrimSpace(v) != "" {
				allBlank = false
				break
			}
		}
		if allBlank {
			continue
		}
		rules[strings.Join(oldVals, "\x00")] = newVals
	}

	return FieldGroup{Name: sheetName, Fields: fields}, rules, true
}

func parseGroupHeaderColumns(headers []string) ([]string, []int, []int, bool) {
	trimmed := make([]string, len(headers))
	for i, h := range headers {
		trimmed[i] = strings.TrimSpace(h)
	}

	nonEmpty := make([]int, 0, len(trimmed))
	for i, h := range trimmed {
		if h != "" {
			nonEmpty = append(nonEmpty, i)
		}
	}
	if len(nonEmpty) < 2 {
		return nil, nil, nil, false
	}

	oldByField := make(map[string]int)
	newByField := make(map[string]int)
	var orderedFields []string
	for _, idx := range nonEmpty {
		h := strings.ToLower(trimmed[idx])
		if strings.HasPrefix(h, "old_") {
			field := strings.TrimSpace(trimmed[idx][4:])
			if field == "" {
				continue
			}
			l := strings.ToLower(field)
			if _, exists := oldByField[l]; !exists {
				orderedFields = append(orderedFields, field)
			}
			oldByField[l] = idx
		} else if strings.HasPrefix(h, "new_") {
			field := strings.TrimSpace(trimmed[idx][4:])
			if field == "" {
				continue
			}
			newByField[strings.ToLower(field)] = idx
		}
	}
	if len(oldByField) > 0 {
		fields := make([]string, 0, len(orderedFields))
		oldCols := make([]int, 0, len(orderedFields))
		newCols := make([]int, 0, len(orderedFields))
		for _, field := range orderedFields {
			l := strings.ToLower(field)
			oldCol, ok := oldByField[l]
			if !ok {
				continue
			}
			fields = append(fields, field)
			oldCols = append(oldCols, oldCol)
			if newCol, ok := newByField[l]; ok {
				newCols = append(newCols, newCol)
			} else {
				newCols = append(newCols, -1)
			}
		}
		if len(fields) > 0 {
			return fields, oldCols, newCols, true
		}
	}

	if len(nonEmpty)%2 != 0 {
		return nil, nil, nil, false
	}
	n := len(nonEmpty) / 2
	fields := make([]string, 0, n)
	oldCols := make([]int, 0, n)
	newCols := make([]int, 0, n)
	for i := 0; i < n; i++ {
		leftCol := nonEmpty[i]
		rightCol := nonEmpty[i+n]
		field := trimmed[leftCol]
		if field == "" {
			return nil, nil, nil, false
		}
		fields = append(fields, field)
		oldCols = append(oldCols, leftCol)
		newCols = append(newCols, rightCol)
	}
	return fields, oldCols, newCols, true
}

func cellAt(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}

// resolveFieldsToCompare returns header names to compare, optionally filtered by configured fields.
// Fields covered by a fieldGroup are excluded from the standalone fieldsToCompare list to avoid double-reporting.
func resolveFieldsToCompare(headers []string, compareFields []string, fieldGroups []FieldGroup) ([]string, []FieldGroup, error) {
	headerMap := make(map[string]string, len(headers))
	for _, h := range headers {
		headerMap[strings.ToLower(strings.TrimSpace(h))] = h
	}

	// Resolve each group's field names to canonical header names
	groupedFields := make(map[string]struct{})
	resolvedGroups := make([]FieldGroup, 0, len(fieldGroups))
	for _, g := range fieldGroups {
		resolved := make([]string, 0, len(g.Fields))
		for _, f := range g.Fields {
			norm := strings.ToLower(strings.TrimSpace(f))
			h, ok := headerMap[norm]
			if !ok {
				return nil, nil, fmt.Errorf("field group %q: field %q not found in Sheet 1 headers", g.Name, f)
			}
			resolved = append(resolved, h)
			groupedFields[h] = struct{}{}
		}
		resolvedGroups = append(resolvedGroups, FieldGroup{Name: g.Name, Fields: resolved})
	}

	// Resolve standalone compare fields (all headers if none specified), excluding grouped fields
	if len(compareFields) == 0 {
		fieldsToCompare := make([]string, 0, len(headers))
		for _, h := range headers {
			if _, inGroup := groupedFields[h]; !inGroup {
				fieldsToCompare = append(fieldsToCompare, h)
			}
		}
		return fieldsToCompare, resolvedGroups, nil
	}

	fieldsToCompare := make([]string, 0, len(compareFields))
	seen := make(map[string]struct{}, len(compareFields))
	for _, field := range compareFields {
		normalized := strings.ToLower(strings.TrimSpace(field))
		header, ok := headerMap[normalized]
		if !ok {
			return nil, nil, fmt.Errorf("field %q not found in Sheet 1 headers", field)
		}
		if _, exists := seen[header]; exists {
			continue
		}
		seen[header] = struct{}{}
		if _, inGroup := groupedFields[header]; !inGroup {
			fieldsToCompare = append(fieldsToCompare, header)
		}
	}

	return fieldsToCompare, resolvedGroups, nil
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

// loadGroupMappingRules reads the "GroupMappings" sheet from the mapping Excel file.
// Expected column layout per group (each group occupies its own set of rows):
//
//	Col 0: GroupName
//	Col 1..N: old field values (one column per field, in the same order as fieldGroups config)
//	Col N+1..2N: new (expected) field values (same field order)
//
// Example for a "Geography" group with fields [country, sub-region, region]:
//
//	GroupName  | country | sub-region | region | country | sub-region | region
//	Geography  | US      | NE         | NA     | USA     | Northeast  | North America
func loadGroupMappingRules(filePath string, fieldGroups []FieldGroup) (GroupMappingRule, error) {
	if len(fieldGroups) == 0 {
		return nil, nil
	}

	f, err := excelize.OpenFile(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	const sheetName = "GroupMappings"
	rows, err := f.GetRows(sheetName)
	if err != nil {
		// Sheet doesn't exist — not an error, just no group mappings
		return nil, nil
	}
	if len(rows) < 2 {
		return nil, nil
	}

	// Build a lookup: lowercase(groupName) -> fieldCount / fieldNames / canonical name
	groupFieldCount := make(map[string]int, len(fieldGroups))
	groupFieldNames := make(map[string][]string, len(fieldGroups))
	groupCanonicalName := make(map[string]string, len(fieldGroups))
	for _, g := range fieldGroups {
		key := strings.ToLower(g.Name)
		groupFieldCount[key] = len(g.Fields)
		groupFieldNames[key] = g.Fields
		groupCanonicalName[key] = g.Name
	}

	rules := make(GroupMappingRule)

	for i := 1; i < len(rows); i++ {
		row := rows[i]
		if len(row) == 0 || strings.TrimSpace(row[0]) == "" {
			continue
		}
		groupKey := strings.ToLower(strings.TrimSpace(row[0]))

		n, ok := groupFieldCount[groupKey]
		if !ok {
			continue // unknown group — skip
		}

		// Need at least 1 (GroupName) + n (old) + n (new) columns
		if len(row) < 1+2*n {
			continue
		}

		fields := groupFieldNames[groupKey]
		canonicalName := groupCanonicalName[groupKey]

		// Build internal key from old-side values
		oldVals := make([]string, n)
		for j := 0; j < n; j++ {
			oldVals[j] = strings.TrimSpace(row[1+j])
		}
		internalKey := strings.Join(oldVals, "\x00")

		// Build new-side field map
		newVals := make(map[string]string, n)
		for j := 0; j < n; j++ {
			newVals[fields[j]] = strings.TrimSpace(row[1+n+j])
		}

		if rules[canonicalName] == nil {
			rules[canonicalName] = make(map[string]map[string]string)
		}
		rules[canonicalName][internalKey] = newVals
	}

	return rules, nil
}

// groupMappingKey builds the internal lookup key from a row's field values for a group.
func groupMappingKey(row map[string]string, fields []string) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = row[f]
	}
	return strings.Join(parts, "\x00")
}

// applyGroupMapping returns the mapped new-side field values for a group if a rule exists,
// otherwise returns nil (meaning: use raw Sheet1 values).
func applyGroupMapping(groupName string, row map[string]string, fields []string, rules GroupMappingRule) map[string]string {
	if rules == nil {
		return nil
	}
	groupRules, ok := rules[groupName]
	if !ok {
		return nil
	}
	key := groupMappingKey(row, fields)
	mapped, ok := groupRules[key]
	if !ok {
		return nil
	}
	return mapped
}

func loadExcelData(filePath, sheetName string, requestedKeyFields []string) ([]string, map[string]map[string]string, map[string]string, []string, error) {
	f, err := excelize.OpenFile(filePath)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	defer f.Close()

	rows, err := f.GetRows(sheetName)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	if len(rows) == 0 {
		return nil, nil, nil, nil, fmt.Errorf("sheet %s is empty", sheetName)
	}

	headers := rows[0]
	keyFields, err := resolveKeyFields(headers, requestedKeyFields)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	data := make(map[string]map[string]string)
	keyDisplay := make(map[string]string)

	for i := 1; i < len(rows); i++ {
		row := rows[i]
		rowData := make(map[string]string)

		for j, header := range headers {
			val := ""
			if j < len(row) {
				val = strings.TrimSpace(row[j])
			}
			rowData[header] = val
		}

		internalKey, isEmpty := buildInternalKey(rowData, keyFields)
		if len(row) == 0 || isEmpty {
			continue // Skip blank rows or rows without key values
		}
		if _, exists := data[internalKey]; exists {
			return nil, nil, nil, nil, fmt.Errorf("duplicate key found in %s/%s: %s", filePath, sheetName, buildDisplayKey(rowData, keyFields))
		}
		data[internalKey] = rowData
		keyDisplay[internalKey] = buildDisplayKey(rowData, keyFields)
	}

	return headers, data, keyDisplay, keyFields, nil
}

// compareData performs the differential analysis and returns collected records
func compareData(fieldsToCompare []string, fieldGroups []FieldGroup, data1, data2 map[string]map[string]string, keyDisplay1 map[string]string, mappingRules MappingRule, groupMappingRules GroupMappingRule, cfg Config, qualExpr QualExpr) ([]DiffRecord, []FieldSummary) {
	var records []DiffRecord
	fieldStats := make(map[string]FieldSummary, len(fieldsToCompare))

	filteredData1 := data1
	if qualExpr != nil {
		filteredData1 = make(map[string]map[string]string)
		for caseKey, row := range data1 {
			if qualExpr.Evaluate(row, cfg.CaseSensitive) {
				filteredData1[caseKey] = row
			}
		}
		fmt.Printf("Case Qualifier applied: %d of %d Sheet 1 records selected.\n\n", len(filteredData1), len(data1))
	}

	fmt.Println("================== ANALYSIS REPORT ==================")

	for _, header := range fieldsToCompare {
		fieldStats[header] = FieldSummary{Field: header}
	}

	for _, row1 := range filteredData1 {
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
	for caseKey := range filteredData1 {
		if _, exists := data2[caseKey]; !exists {
			displayKey := keyDisplay1[caseKey]
			fmt.Printf("  [Missing] Key: %s\n", displayKey)
			records = append(records, DiffRecord{
				CaseID: displayKey,
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

	for caseKey, row1 := range filteredData1 {
		row2, exists := data2[caseKey]
		if !exists {
			continue
		}
		commonCount++
		caseHasDiff := false
		displayKey := keyDisplay1[caseKey]

		// 2a. Compare field groups as a unit
		for _, group := range fieldGroups {
			val1Parts := make([]string, 0, len(group.Fields))
			val2Parts := make([]string, 0, len(group.Fields))
			groupMismatch := false

			// Apply group-level mapping: if the old-side combination has a rule, substitute mapped values
			mappedGroupVals := applyGroupMapping(group.Name, row1, group.Fields, groupMappingRules)

			for _, header := range group.Fields {
				v1 := row1[header]
				v2 := row2[header]

				// Determine the effective Sheet1 value for comparison:
				// group mapping takes precedence over per-field mapping
				var effectiveV1 string
				if mappedGroupVals != nil {
					effectiveV1 = mappedGroupVals[header]
				} else {
					effectiveV1 = applyMapping(header, v1, mappingRules)
				}

				displayV1 := effectiveV1
				if effectiveV1 != v1 {
					displayV1 = fmt.Sprintf("%s (mapped to %s)", v1, effectiveV1)
				}
				val1Parts = append(val1Parts, fmt.Sprintf("%s=%s", header, displayV1))
				val2Parts = append(val2Parts, fmt.Sprintf("%s=%s", header, v2))
				if !compareValues(effectiveV1, v2, header, cfg) {
					groupMismatch = true
				}
			}

			if groupMismatch {
				caseHasDiff = true
				sheet1Val := strings.Join(val1Parts, ", ")
				sheet2Val := strings.Join(val2Parts, ", ")
				records = append(records, DiffRecord{
					CaseID: displayKey,
					Field:  group.Name,
					Val1:   sheet1Val,
					Val2:   sheet2Val,
					Status: "Mismatch",
				})
				fmt.Printf("    * Group [%s] mismatch for Key %s:\n      Sheet1: %s\n      Sheet2: %s\n", group.Name, displayKey, sheet1Val, sheet2Val)
			}
		}

		// 2b. Compare standalone fields individually
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
					CaseID: displayKey,
					Field:  header,
					Val1:   reportVal1,
					Val2:   val2,
					Status: "Mismatch",
				})
				fmt.Printf("    * Field [%s] mismatch for Key %s: Sheet1='%s' vs Sheet2='%s'\n", header, displayKey, reportVal1, val2)
			}
		}

		if caseHasDiff {
			diffCount++
		}
	}

	fmt.Println("\n================== SUMMARY ==================")
	fmt.Printf("Total Cases in Sheet 1: %d\n", len(data1))
	if qualExpr != nil {
		fmt.Printf("Cases After Qualifier:    %d\n", len(filteredData1))
	}
	fmt.Printf("Total Cases in Sheet 2:   %d\n", len(data2))
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

func compareDataGrouped(fieldsToCompare []string, fieldGroups []FieldGroup, data1, data2 map[string]map[string]string, keyDisplay1 map[string]string, mappingRules MappingRule, groupMappingRules GroupMappingRule, cfg Config, qualExpr QualExpr) []GroupedSheetResult {
	filteredData1 := data1
	if qualExpr != nil {
		filteredData1 = make(map[string]map[string]string)
		for caseKey, row := range data1 {
			if qualExpr.Evaluate(row, cfg.CaseSensitive) {
				filteredData1[caseKey] = row
			}
		}
	}

	singleSheet := GroupedSheetResult{SheetName: "SingleFields"}
	groupSheets := make(map[string]*GroupedSheetResult, len(fieldGroups))

	for _, group := range fieldGroups {
		groupSheets[group.Name] = &GroupedSheetResult{SheetName: group.Name}
	}

	keys := sortedMapKeys(filteredData1)
	for _, caseKey := range keys {
		row1 := filteredData1[caseKey]
		row2, exists := data2[caseKey]
		displayKey := keyDisplay1[caseKey]
		if !exists {
			for _, header := range fieldsToCompare {
				rec := buildGroupedDiffRecord(displayKey, header, applyMapping(header, row1[header], mappingRules), "", cfg)
				singleSheet.Records = append(singleSheet.Records, maskGroupedRecordValues(rec, cfg.ShowMatchedValues))
			}
			for _, group := range fieldGroups {
				for _, header := range group.Fields {
					effective := applyMapping(header, row1[header], mappingRules)
					if mapped := applyGroupMapping(group.Name, row1, group.Fields, groupMappingRules); mapped != nil {
						effective = mapped[header]
					}
					rec := buildGroupedDiffRecord(displayKey, header, effective, "", cfg)
					groupSheets[group.Name].Records = append(groupSheets[group.Name].Records, maskGroupedRecordValues(rec, cfg.ShowMatchedValues))
				}
			}
			continue
		}

		for _, header := range fieldsToCompare {
			effective := applyMapping(header, row1[header], mappingRules)
			rec := buildGroupedDiffRecord(displayKey, header, effective, row2[header], cfg)
			singleSheet.Records = append(singleSheet.Records, maskGroupedRecordValues(rec, cfg.ShowMatchedValues))
		}

		for _, group := range fieldGroups {
			mappedGroupVals := applyGroupMapping(group.Name, row1, group.Fields, groupMappingRules)
			for _, header := range group.Fields {
				effective := applyMapping(header, row1[header], mappingRules)
				if mappedGroupVals != nil {
					effective = mappedGroupVals[header]
				}
				rec := buildGroupedDiffRecord(displayKey, header, effective, row2[header], cfg)
				groupSheets[group.Name].Records = append(groupSheets[group.Name].Records, maskGroupedRecordValues(rec, cfg.ShowMatchedValues))
			}
		}
	}

	finalResults := make([]GroupedSheetResult, 0, 1+len(fieldGroups))
	finalResults = append(finalResults, singleSheet)
	for _, group := range fieldGroups {
		if sheet, ok := groupSheets[group.Name]; ok {
			finalResults = append(finalResults, *sheet)
		}
	}
	return finalResults
}

func sortedMapKeys(m map[string]map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func buildGroupedDiffRecord(caseID, field, oldRaw, newRaw string, cfg Config) GroupedDiffRecord {
	oldNorm := normalizeValueForComparison(oldRaw, field, cfg)
	newNorm := normalizeValueForComparison(newRaw, field, cfg)
	rawMatch := oldRaw == newRaw
	normalizedMatch := oldNorm == newNorm
	return GroupedDiffRecord{
		CaseID:             caseID,
		Field:              field,
		OldRawValue:        oldRaw,
		NewRawValue:        newRaw,
		RawMatch:           rawMatch,
		OldNormalizedValue: oldNorm,
		NewNormalizedValue: newNorm,
		NormalizedMatch:    normalizedMatch,
		FinalStatus:        classifyFinalStatus(rawMatch, normalizedMatch),
	}
}

func classifyFinalStatus(rawMatch, normalizedMatch bool) string {
	switch {
	case rawMatch && normalizedMatch:
		return "FULL_MATCH"
	case !rawMatch && normalizedMatch:
		return "MATCH_AFTER_NORMALIZATION"
	case rawMatch && !normalizedMatch:
		return "RAW_MATCH_ONLY"
	default:
		return "MISMATCH"
	}
}

func normalizeValueForComparison(val, header string, cfg Config) string {
	normalized := val
	if !cfg.CaseSensitive {
		normalized = strings.ToLower(normalized)
	}
	if cfg.NormalizeList {
		normalized = normalizeListString(normalized)
	}
	if !cfg.StrictDate && isDateHeaderOrValue(header, val) {
		if parsed, err := parseDate(val); err == nil {
			normalized = parsed.Format("2006-01-02")
		}
	}
	return normalized
}

func maskGroupedRecordValues(rec GroupedDiffRecord, showMatchedValues bool) GroupedDiffRecord {
	if showMatchedValues {
		return rec
	}
	if rec.FinalStatus == "MISMATCH" {
		return rec
	}
	rec.OldRawValue = ""
	rec.NewRawValue = ""
	rec.OldNormalizedValue = ""
	rec.NewNormalizedValue = ""
	return rec
}

func resolveKeyFields(headers, requestedKeyFields []string) ([]string, error) {
	if len(headers) == 0 {
		return nil, fmt.Errorf("missing header row")
	}
	if len(requestedKeyFields) == 0 {
		return []string{headers[0]}, nil
	}

	headerMap := make(map[string]string, len(headers))
	for _, h := range headers {
		headerMap[strings.ToLower(strings.TrimSpace(h))] = h
	}

	resolved := make([]string, 0, len(requestedKeyFields))
	seen := make(map[string]struct{}, len(requestedKeyFields))
	for _, f := range requestedKeyFields {
		norm := strings.ToLower(strings.TrimSpace(f))
		h, ok := headerMap[norm]
		if !ok {
			return nil, fmt.Errorf("key field %q not found in headers", f)
		}
		if _, exists := seen[h]; exists {
			continue
		}
		seen[h] = struct{}{}
		resolved = append(resolved, h)
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("at least one key field is required")
	}
	return resolved, nil
}

func buildInternalKey(rowData map[string]string, keyFields []string) (string, bool) {
	parts := make([]string, len(keyFields))
	empty := true
	for i, field := range keyFields {
		v := strings.TrimSpace(rowData[field])
		parts[i] = v
		if v != "" {
			empty = false
		}
	}
	return strings.Join(parts, "\x00"), empty
}

func buildDisplayKey(rowData map[string]string, keyFields []string) string {
	if len(keyFields) == 1 {
		return rowData[keyFields[0]]
	}
	parts := make([]string, 0, len(keyFields))
	for _, field := range keyFields {
		parts = append(parts, fmt.Sprintf("%s=%s", field, rowData[field]))
	}
	return strings.Join(parts, ", ")
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

// writeReportToExcel creates the final Excel workbook with a comparison sheet and field-wise analysis sheet.
func writeReportToExcel(outputPath string, records []DiffRecord, summaries []FieldSummary) error {
	f := excelize.NewFile()
	sheetName := "Comparison Report"
	summarySheetName := "Field-wise Analysis"

	index, err := f.NewSheet(sheetName)
	if err != nil {
		return err
	}
	if _, err := f.NewSheet(summarySheetName); err != nil {
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

	f.SetCellValue(summarySheetName, "A1", "Field-wise Analysis")
	if err := f.SetCellStyle(summarySheetName, "A1", "A1", headerStyle); err != nil {
		return err
	}

	summaryHeaderRow := 2
	summaryHeaders := []string{"Field", "Populated in Sheet1", "Populated Rows with Case in Sheet2", "Mismatch Count", "Mismatch %", "RAG Status"}
	for colIdx, header := range summaryHeaders {
		cell, _ := excelize.CoordinatesToCellName(colIdx+1, summaryHeaderRow)
		f.SetCellValue(summarySheetName, cell, header)
	}
	if err := f.SetCellStyle(summarySheetName, fmt.Sprintf("A%d", summaryHeaderRow), fmt.Sprintf("F%d", summaryHeaderRow), headerStyle); err != nil {
		return err
	}

	for i, summary := range summaries {
		rowNum := summaryHeaderRow + i + 1
		f.SetCellValue(summarySheetName, fmt.Sprintf("A%d", rowNum), summary.Field)
		f.SetCellValue(summarySheetName, fmt.Sprintf("B%d", rowNum), summary.PopulatedInSource)
		f.SetCellValue(summarySheetName, fmt.Sprintf("C%d", rowNum), summary.ComparedCount)
		f.SetCellValue(summarySheetName, fmt.Sprintf("D%d", rowNum), summary.MismatchCount)

		mismatchPercent := 0.0
		if summary.ComparedCount > 0 {
			mismatchPercent = float64(summary.MismatchCount) / float64(summary.ComparedCount) * 100
		}
		f.SetCellValue(summarySheetName, fmt.Sprintf("E%d", rowNum), fmt.Sprintf("%.2f%%", mismatchPercent))

		ragLabel, ragStyle := determineRAGStatus(summary.ComparedCount, summary.MismatchCount, redStyle, amberStyle, greenStyle)
		f.SetCellValue(summarySheetName, fmt.Sprintf("F%d", rowNum), ragLabel)
		if err := f.SetCellStyle(summarySheetName, fmt.Sprintf("F%d", rowNum), fmt.Sprintf("F%d", rowNum), ragStyle); err != nil {
			return err
		}
	}

	if err := f.SetColWidth(sheetName, "A", "A", 24); err != nil {
		return err
	}
	if err := f.SetColWidth(sheetName, "B", "F", 22); err != nil {
		return err
	}
	if err := f.SetColWidth(summarySheetName, "A", "A", 24); err != nil {
		return err
	}
	if err := f.SetColWidth(summarySheetName, "B", "F", 22); err != nil {
		return err
	}

	if err := f.SaveAs(outputPath); err != nil {
		return err
	}

	return nil
}

func writeGroupedReportToExcel(outputPath string, sheets []GroupedSheetResult) error {
	f := excelize.NewFile()
	defaultSheet := f.GetSheetName(0)

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

	sheetOrder := buildOutputSheetOrder(sheets)
	for i, sheet := range sheetOrder {
		index, err := f.NewSheet(sheet)
		if err != nil {
			return err
		}
		if i == 0 {
			f.SetActiveSheet(index)
		}
	}
	if defaultSheet != "" {
		f.DeleteSheet(defaultSheet)
	}

	headers := []string{"case_id", "field", "old_raw_value", "new_raw_value", "raw_match", "old_normalized_value", "new_normalized_value", "normalized_match", "final_status"}
	for i, sheetResult := range sheets {
		sheetName := sheetOrder[i]

		for c, h := range headers {
			cell, _ := excelize.CoordinatesToCellName(c+1, 1)
			f.SetCellValue(sheetName, cell, h)
		}
		if err := f.SetCellStyle(sheetName, "A1", "I1", headerStyle); err != nil {
			return err
		}
		for r, rec := range sheetResult.Records {
			rowNum := r + 2
			f.SetCellValue(sheetName, fmt.Sprintf("A%d", rowNum), rec.CaseID)
			f.SetCellValue(sheetName, fmt.Sprintf("B%d", rowNum), rec.Field)
			f.SetCellValue(sheetName, fmt.Sprintf("C%d", rowNum), rec.OldRawValue)
			f.SetCellValue(sheetName, fmt.Sprintf("D%d", rowNum), rec.NewRawValue)
			f.SetCellValue(sheetName, fmt.Sprintf("E%d", rowNum), rec.RawMatch)
			f.SetCellValue(sheetName, fmt.Sprintf("F%d", rowNum), rec.OldNormalizedValue)
			f.SetCellValue(sheetName, fmt.Sprintf("G%d", rowNum), rec.NewNormalizedValue)
			f.SetCellValue(sheetName, fmt.Sprintf("H%d", rowNum), rec.NormalizedMatch)
			f.SetCellValue(sheetName, fmt.Sprintf("I%d", rowNum), rec.FinalStatus)
		}
		_ = f.SetColWidth(sheetName, "A", "I", 22)
	}

	return f.SaveAs(outputPath)
}

func buildOutputSheetOrder(sheets []GroupedSheetResult) []string {
	input := make([]string, 0, len(sheets))
	for _, s := range sheets {
		input = append(input, s.SheetName)
	}
	return sanitizeAndDedupeSheetNames(input)
}

func sanitizeAndDedupeSheetNames(names []string) []string {
	out := make([]string, 0, len(names))
	used := make(map[string]struct{})
	baseCounts := make(map[string]int)
	for _, n := range names {
		base := sanitizeExcelSheetName(n)
		baseCounts[base]++
		candidate := base
		if _, exists := used[candidate]; exists {
			for i := baseCounts[base]; ; i++ {
				suffix := fmt.Sprintf("_%d", i)
				candidate = truncateSheetName(base, 31-len(suffix)) + suffix
				if _, exists := used[candidate]; !exists {
					break
				}
			}
		}
		used[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func sanitizeExcelSheetName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		trimmed = "Sheet"
	}
	replacer := strings.NewReplacer(
		":", "_",
		"\\", "_",
		"/", "_",
		"?", "_",
		"*", "_",
		"[", "_",
		"]", "_",
	)
	clean := replacer.Replace(trimmed)
	clean = strings.Trim(clean, "'")
	if clean == "" {
		clean = "Sheet"
	}
	return truncateSheetName(clean, 31)
}

func truncateSheetName(name string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(name)
	if len(runes) <= maxLen {
		return name
	}
	return string(runes[:maxLen])
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
