package main

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestLegacyReportHeadersUnchanged(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "legacy_report.xlsx")
	records := []DiffRecord{{CaseID: "1", Field: "status", Val1: "A", Val2: "B", Status: "Mismatch"}}
	summaries := []FieldSummary{{Field: "status", PopulatedInSource: 1, ComparedCount: 1, MismatchCount: 1}}

	if err := writeReportToExcel(outPath, records, summaries); err != nil {
		t.Fatalf("writeReportToExcel failed: %v", err)
	}

	f, err := excelize.OpenFile(outPath)
	if err != nil {
		t.Fatalf("open output failed: %v", err)
	}
	defer f.Close()

	if got := f.GetSheetList(); len(got) == 0 || got[0] != "Comparison Report" {
		t.Fatalf("unexpected sheet list: %v", got)
	}

	headers, err := f.GetRows("Comparison Report")
	if err != nil {
		t.Fatalf("read rows failed: %v", err)
	}
	want := []string{"CaseID", "Field", "Sheet1 Value", "Sheet2 Value", "Status"}
	if !reflect.DeepEqual(headers[0], want) {
		t.Fatalf("legacy header changed, got=%v want=%v", headers[0], want)
	}
}

func TestLegacyReportWritesFieldWiseAnalysisToDedicatedSheet(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "legacy_report.xlsx")
	records := []DiffRecord{{CaseID: "1", Field: "status", Val1: "A", Val2: "B", Status: "Mismatch"}}
	summaries := []FieldSummary{{Field: "status", PopulatedInSource: 1, ComparedCount: 1, MismatchCount: 1}}

	if err := writeReportToExcel(outPath, records, summaries); err != nil {
		t.Fatalf("writeReportToExcel failed: %v", err)
	}

	f, err := excelize.OpenFile(outPath)
	if err != nil {
		t.Fatalf("open output failed: %v", err)
	}
	defer f.Close()

	if got, want := f.GetSheetList(), []string{"Comparison Report", "Field-wise Analysis"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected sheet list got=%v want=%v", got, want)
	}

	reportRows, err := f.GetRows("Comparison Report")
	if err != nil {
		t.Fatalf("read comparison report failed: %v", err)
	}
	if got, want := len(reportRows), 2; got != want {
		t.Fatalf("expected comparison report rows without embedded summary got=%d want=%d", got, want)
	}

	summaryRows, err := f.GetRows("Field-wise Analysis")
	if err != nil {
		t.Fatalf("read field-wise analysis failed: %v", err)
	}
	if got, want := summaryRows[0], []string{"Field-wise Analysis"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected summary title row got=%v want=%v", got, want)
	}
	if got, want := summaryRows[1], []string{"Field", "Populated in Sheet1", "Populated Rows with Case in Sheet2", "Mismatch Count", "Mismatch %", "RAG Status"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected summary header row got=%v want=%v", got, want)
	}
	if got, want := summaryRows[2], []string{"status", "1", "1", "1", "100.00%", "Red"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected summary data row got=%v want=%v", got, want)
	}
}

func TestLoadGroupedMappingWorkbookParsesAndPreservesOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mapping.xlsx")
	f := excelize.NewFile()
	f.SetSheetName("Sheet1", "SingleFields")
	f.NewSheet("Geography")
	f.NewSheet("Classification")

	_ = f.SetSheetRow("SingleFields", "A1", &[]string{"FieldName", "OldValue", "NewValue"})
	_ = f.SetSheetRow("SingleFields", "A2", &[]string{"status", "Active", "ACTIVE"})

	_ = f.SetSheetRow("Geography", "A1", &[]string{"country", "region", "country", "region"})
	_ = f.SetSheetRow("Geography", "A2", &[]string{"US", "NA", "USA", "North America"})

	_ = f.SetSheetRow("Classification", "A1", &[]string{"old_category", "old_sub_category", "new_category", "new_sub_category"})
	_ = f.SetSheetRow("Classification", "A2", &[]string{"Tech", "HW", "Technology", "Hardware"})

	if err := f.SaveAs(path); err != nil {
		t.Fatalf("save mapping workbook failed: %v", err)
	}

	wb, groupedMode, err := loadGroupedMappingWorkbook(path)
	if err != nil {
		t.Fatalf("loadGroupedMappingWorkbook failed: %v", err)
	}
	if !groupedMode {
		t.Fatalf("expected grouped mode")
	}

	if got, want := len(wb.Groups), 2; got != want {
		t.Fatalf("unexpected group count got=%d want=%d", got, want)
	}
	if wb.Groups[0].Name != "Geography" || wb.Groups[1].Name != "Classification" {
		t.Fatalf("group order not preserved: %+v", wb.Groups)
	}
	if wb.SingleFieldRules["status"]["Active"] != "ACTIVE" {
		t.Fatalf("single field rule not parsed")
	}
	if wb.GroupRules["Geography"]["US\x00NA"]["country"] != "USA" {
		t.Fatalf("group rule not parsed")
	}
	if wb.GroupRules["Classification"]["Tech\x00HW"]["sub_category"] != "Hardware" {
		t.Fatalf("prefixed header parsing failed")
	}
}

func TestLoadGroupedMappingWorkbookLegacyReturnsFalse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy_mapping.xlsx")
	f := excelize.NewFile()
	_ = f.SetSheetRow("Sheet1", "A1", &[]string{"FieldName", "OldValue", "NewValue"})
	_ = f.SetSheetRow("Sheet1", "A2", &[]string{"status", "A", "B"})
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("save workbook failed: %v", err)
	}

	_, groupedMode, err := loadGroupedMappingWorkbook(path)
	if err != nil {
		t.Fatalf("loadGroupedMappingWorkbook failed: %v", err)
	}
	if groupedMode {
		t.Fatalf("expected legacy mode when SingleFields sheet is absent")
	}
}

func TestSheetNameSanitizeAndDedupe(t *testing.T) {
	got := sanitizeAndDedupeSheetNames([]string{"SingleFields", "A/B", "A:B", "", "A_B"})
	want := []string{"SingleFields", "A_B", "A_B_2", "Sheet", "A_B_3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected sanitize/dedupe result got=%v want=%v", got, want)
	}
}

func TestGroupedReportSheetOrderAndSanitizedNames(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "grouped_report.xlsx")
	sheets := []GroupedSheetResult{
		{SheetName: "SingleFields", Records: []GroupedDiffRecord{{CaseID: "k1", Field: "status", FinalStatus: "MISMATCH"}}},
		{SheetName: "A/B"},
		{SheetName: "A:B"},
	}
	if err := writeGroupedReportToExcel(outPath, sheets); err != nil {
		t.Fatalf("writeGroupedReportToExcel failed: %v", err)
	}

	f, err := excelize.OpenFile(outPath)
	if err != nil {
		t.Fatalf("open grouped output failed: %v", err)
	}
	defer f.Close()

	gotSheets := f.GetSheetList()
	wantSheets := []string{"SingleFields", "A_B", "A_B_2"}
	if !reflect.DeepEqual(gotSheets, wantSheets) {
		t.Fatalf("unexpected sheet order/names got=%v want=%v", gotSheets, wantSheets)
	}
}

func TestFinalStatusMatrix(t *testing.T) {
	cases := []struct {
		raw, normalized, final bool
		want                   string
	}{
		{true, true, true, "FULL_MATCH"},
		{false, true, true, "MATCH_AFTER_NORMALIZATION"},
		{true, false, true, "RAW_MATCH_ONLY"},
		{false, false, true, "MATCH_BY_COMPARATOR"},
		{true, true, false, "MISMATCH"},
		{true, false, false, "MISMATCH"},
		{false, true, false, "MISMATCH"},
		{false, false, false, "MISMATCH"},
	}

	for _, tc := range cases {
		if got := classifyFinalStatus(tc.raw, tc.normalized, tc.final); got != tc.want {
			t.Fatalf("classifyFinalStatus(%v,%v,%v)=%s want=%s", tc.raw, tc.normalized, tc.final, got, tc.want)
		}
	}
}

func TestSingleFieldsMatchedValueVisibilityAndStatuses(t *testing.T) {
	data1 := map[string]map[string]string{
		"k1": {"status": "Active", "city": "Boston"},
	}
	data2 := map[string]map[string]string{
		"k1": {"status": "active", "city": "Chicago"},
	}
	keyDisplay := map[string]string{"k1": "k1"}

	hidden := compareDataGrouped([]string{"status", "city"}, nil, data1, data2, keyDisplay, nil, nil, Config{}, nil)
	if len(hidden) != 1 || hidden[0].SheetName != "SingleFields" {
		t.Fatalf("unexpected grouped results: %+v", hidden)
	}
	if got, want := len(hidden[0].Records), 1; got != want {
		t.Fatalf("showMatchedValues=false should only include mismatches got=%d want=%d", got, want)
	}
	if got := hidden[0].Records[0].Field; got != "city" {
		t.Fatalf("expected only mismatched field to be visible, got %s", got)
	}

	shownCfg := Config{ShowMatchedValues: true}
	shown := compareDataGrouped([]string{"status", "city"}, nil, data1, data2, keyDisplay, nil, nil, shownCfg, nil)
	if got, want := len(shown[0].Records), 2; got != want {
		t.Fatalf("showMatchedValues=true should include matches and mismatches got=%d want=%d", got, want)
	}

	var statusRec, cityRec *GroupedDiffRecord
	for i := range shown[0].Records {
		rec := &shown[0].Records[i]
		switch rec.Field {
		case "status":
			statusRec = rec
		case "city":
			cityRec = rec
		}
	}
	if statusRec == nil || cityRec == nil {
		t.Fatalf("expected both status and city records, got %+v", shown[0].Records)
	}

	if statusRec.RawMatch {
		t.Fatalf("status should be raw mismatch, got %+v", *statusRec)
	}
	if !statusRec.NormalizedMatch || !statusRec.FinalMatch {
		t.Fatalf("status should match after normalization and in final result, got %+v", *statusRec)
	}
	if statusRec.FinalStatus != "MATCH_AFTER_NORMALIZATION" || statusRec.ComparisonReason != "equal_after_normalization" {
		t.Fatalf("unexpected status diagnostics for status field: %+v", *statusRec)
	}

	if cityRec.FinalMatch || cityRec.FinalStatus != "MISMATCH" || cityRec.ComparisonReason != "mismatch" {
		t.Fatalf("unexpected mismatch diagnostics for city field: %+v", *cityRec)
	}
}

func TestGroupedReportHeadersIncludeComparisonStateColumns(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "grouped_report.xlsx")
	sheets := []GroupedSheetResult{
		{
			SheetName: "SingleFields",
			Records: []GroupedDiffRecord{
				{
					CaseID:             "k1",
					Field:              "status",
					OldRawValue:        "A",
					NewRawValue:        "a",
					RawMatch:           false,
					OldNormalizedValue: "a",
					NewNormalizedValue: "a",
					NormalizedMatch:    true,
					FinalMatch:         true,
					FinalStatus:        "MATCH_AFTER_NORMALIZATION",
					ComparisonReason:   "equal_after_normalization",
				},
			},
		},
	}
	if err := writeGroupedReportToExcel(outPath, sheets); err != nil {
		t.Fatalf("writeGroupedReportToExcel failed: %v", err)
	}
	f, err := excelize.OpenFile(outPath)
	if err != nil {
		t.Fatalf("open grouped output failed: %v", err)
	}
	defer f.Close()

	rows, err := f.GetRows("SingleFields")
	if err != nil {
		t.Fatalf("read rows failed: %v", err)
	}
	wantHeaders := []string{"case_id", "field", "old_raw_value", "new_raw_value", "raw_match", "old_normalized_value", "new_normalized_value", "normalized_match", "final_match", "final_status", "comparison_reason"}
	if !reflect.DeepEqual(rows[0], wantHeaders) {
		t.Fatalf("unexpected grouped header row got=%v want=%v", rows[0], wantHeaders)
	}
	if got, want := rows[1][8], "TRUE"; got != want {
		t.Fatalf("unexpected final_match cell got=%s want=%s", got, want)
	}
	if got, want := rows[1][10], "equal_after_normalization"; got != want {
		t.Fatalf("unexpected comparison_reason cell got=%s want=%s", got, want)
	}
}
