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
		raw, normalized bool
		want            string
	}{
		{true, true, "FULL_MATCH"},
		{false, true, "MATCH_AFTER_NORMALIZATION"},
		{true, false, "RAW_MATCH_ONLY"},
		{false, false, "MISMATCH"},
	}

	for _, tc := range cases {
		if got := classifyFinalStatus(tc.raw, tc.normalized); got != tc.want {
			t.Fatalf("classifyFinalStatus(%v,%v)=%s want=%s", tc.raw, tc.normalized, got, tc.want)
		}
	}
}

func TestSingleFieldsMatchedValueVisibilityToggle(t *testing.T) {
	cases := []struct {
		name       string
		oldValue   string
		newValue   string
		cfg        Config
		wantStatus string
	}{
		{
			name:       "full match",
			oldValue:   "Active",
			newValue:   "Active",
			cfg:        Config{},
			wantStatus: "FULL_MATCH",
		},
		{
			name:       "match after normalization",
			oldValue:   "Active",
			newValue:   "active",
			cfg:        Config{},
			wantStatus: "MATCH_AFTER_NORMALIZATION",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data1 := map[string]map[string]string{"k1": {"status": tc.oldValue}}
			data2 := map[string]map[string]string{"k1": {"status": tc.newValue}}
			keyDisplay := map[string]string{"k1": "k1"}

			cfgHidden := tc.cfg
			resultsHidden := compareDataGrouped([]string{"status"}, nil, data1, data2, keyDisplay, nil, nil, cfgHidden, nil)
			if len(resultsHidden) != 1 || resultsHidden[0].SheetName != "SingleFields" || len(resultsHidden[0].Records) != 1 {
				t.Fatalf("unexpected grouped results: %+v", resultsHidden)
			}
			recHidden := resultsHidden[0].Records[0]
			if recHidden.FinalStatus != tc.wantStatus {
				t.Fatalf("unexpected status: %s", recHidden.FinalStatus)
			}
			if recHidden.OldRawValue != "" || recHidden.NewRawValue != "" || recHidden.OldNormalizedValue != "" || recHidden.NewNormalizedValue != "" {
				t.Fatalf("expected matched values hidden, got %+v", recHidden)
			}

			cfgShown := tc.cfg
			cfgShown.ShowMatchedValues = true
			resultsShown := compareDataGrouped([]string{"status"}, nil, data1, data2, keyDisplay, nil, nil, cfgShown, nil)
			recShown := resultsShown[0].Records[0]
			if recShown.FinalStatus != tc.wantStatus {
				t.Fatalf("unexpected shown status: %s", recShown.FinalStatus)
			}
			if recShown.OldRawValue != tc.oldValue || recShown.NewRawValue != tc.newValue {
				t.Fatalf("expected raw values visible, got %+v", recShown)
			}
			if recShown.OldNormalizedValue == "" || recShown.NewNormalizedValue == "" {
				t.Fatalf("expected normalized values visible, got %+v", recShown)
			}
		})
	}
}
