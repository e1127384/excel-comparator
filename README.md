# Excel Row Comparator CLI in Go

A robust, high-performance command-line interface (CLI) application written in Go designed to compare two Excel sheets row-by-row based on a primary key (`caseid` in the first column).

It handles missing records, field-by-field discrepancies, flexible date normalization (supporting text/general formats like `06 Aug 2025` and `10-Mar-2027` without time components), list separator normalization (e.g., `,` vs `;`), data migration mapping transformations, and case-sensitivity toggles, exporting all findings cleanly into an output Excel report.

---

## Features

* **Primary Key Mapping**: Uses the first column as the unique `CaseID`.
* **Missing Record Analysis**: Identifies cases present in Sheet 1 but completely missing in Sheet 2.
* **Field-by-Field Mismatch Detection**: Compares every common field column-by-column.
* **Migration Mapping Rules (`mappingFile`)**: Translates old field values to new expected migration values using an external mapping spreadsheet (`FieldName`, `OldValue`, `NewValue`).
* **Date Normalization (`strictDate`)**: Automatically parses and equates general/text date formats like `06 Aug 2025` and `10-Mar-2027`, while ignoring time components.
* **List Separator Normalization (`normalizeList`)**: Treats comma-separated and semicolon-separated items (e.g., `a, b` vs `a; b`) identically.
* **Case Sensitivity (`caseSensitive`)**: Configures whether string comparisons ignore letter casing.
* **Field Selection (`compareFields`)**: Lets you compare only a specific list of fields.
* **Excel Export**: Writes a clean summary report containing columns: `CaseID`, `Field`, `Sheet1 Value`, `Sheet2 Value`, and `Status`.

---

## Prerequisites

Make sure you have the following installed on your machine:

* **Go** (version 1.18 or higher recommended)

---

## Project Setup & Installation

1. **Create and enter your project directory:**
```bash
mkdir excel-comparator
cd excel-comparator

```


2. **Initialize the Go module:**
```bash
go mod init excel-comparator

```


3. **Install the required Excel processing library (`excelize`):**
```bash
go get github.com/xuri/excelize/v2

```


4. **Add the Application Code:**
Save the Go source code into a file named `main.go` in your project root directory.

---

## Configuration File

The CLI now reads parameters from a YAML config file.

### Runtime Flag

| Flag | Default | Description |
| --- | --- | --- |
| `-config` | `"config.yaml"` | Path to the YAML configuration file. |

### Config YAML Fields

| Key | Default | Description |
| --- | --- | --- |
| `file1` | `""` | **(Required)** Path to the first Excel file. |
| `file2` | `""` | **(Required)** Path to the second Excel file. |
| `sheet1` | `"Sheet1"` | Sheet name for File 1. |
| `sheet2` | `"Sheet1"` | Sheet name for File 2. |
| `mappingFile` | `""` | Path to migration mapping Excel file (`FieldName`, `OldValue`, `NewValue`). |
| `outputFile` | `"comparison_report.xlsx"` | File path for generated report. |
| `caseSensitive` | `false` | Enable or disable case-sensitive comparison. |
| `strictDate` | `false` | Enable or disable strict raw string date comparison. |
| `normalizeList` | `true` | Treat comma and semicolon lists as equal. |
| `compareFields` | `[]` | Optional list of field names to compare. Empty means compare all headers. |
| `fieldGroups` | `[]` | Optional list of named field groups compared as a logical unit (see below). |

Example `config.yaml`:

```yaml
file1: file1.xlsx
file2: file2.xlsx
sheet1: SheetA
sheet2: SheetB
mappingFile: migration_rules.xlsx
outputFile: comparison_report.xlsx
caseSensitive: false
strictDate: false
normalizeList: true
compareFields:
  - caseid
  - status
  - start_date
fieldGroups:
  - name: "Geography"
    fields: [country, sub-region, region]
  - name: "Classification"
    fields: [category, sub-category]
```

### Field Groups

`fieldGroups` lets you compare a combination of related fields as a single logical unit. If **any** field in the group differs, one row is written to the report using the group name — instead of a separate row per field.

Fields that belong to a group are automatically excluded from standalone `compareFields` comparison to avoid duplicate reporting.

---

## Migration Mapping Rules

Set `mappingFile` in `config.yaml` to an Excel file that contains two types of mapping sheets:

### Sheet 1 (first sheet) — Per-Field Mappings

Translates a single field's old value to the expected new value.

| FieldName | OldValue | NewValue |
|-----------|----------|----------|
| status    | Active   | ACTIVE   |
| region    | NA       | North America |

### Sheet `GroupMappings` — Group-Level Combination Mappings

Maps an entire combination of field values (for a named field group) from old to new. Each field occupies its own column — **N old-value columns followed by N new-value columns**, matching the field order defined in `fieldGroups`.

**Column layout:** `GroupName | field1 (old) | field2 (old) | … | field1 (new) | field2 (new) | …`

Example for a `Geography` group with fields `[country, sub-region, region]`:

| GroupName | country | sub-region | region | country | sub-region | region |
|-----------|---------|------------|--------|---------|------------|--------|
| Geography | US      | NE         | NA     | USA     | Northeast  | North America |
| Geography | UK      | SE         | EU     | GBR     | South East | Europe |

Example for a `Classification` group with fields `[category, sub-category]`:

| GroupName      | category | sub-category | category   | sub-category |
|----------------|----------|--------------|------------|--------------|
| Classification | Tech     | HW           | Technology | Hardware     |

> **Note:** The `GroupMappings` sheet is optional. If it is absent from the mapping file, group fields fall back to per-field mapping rules (or raw comparison).



You can start from the committed template:

```bash
cp config.yaml.example config.yaml
```

---

## Running the Application

### 1. Basic Comparison (Default Settings)

Performs case-insensitive checks, handles text date variations (like `06 Aug 2025`), strips time components, and normalizes list punctuation:

```bash
go run main.go
```

Or provide a custom path:

```bash
go run main.go -config /path/to/config.yaml
```

### 2. Running with Migration Mapping Rules

If you are tracking field value migrations or transformations (e.g., changing status codes or mapping values across columns):

Set `mappingFile: migration_rules.xlsx` in `config.yaml`.

*Note: The migration mapping file (`migration_rules.xlsx`) must contain three columns: `FieldName`, `OldValue`, and `NewValue`.*

### 3. Specifying Custom Sheet Names

If your target data lives on custom worksheet tabs:

Set `sheet1` and `sheet2` in `config.yaml`.

### 4. Strict Comparison Mode

Enforces strict checks (exact case matching, exact date strings, and literal list separators):

Set values in `config.yaml`:

```yaml
caseSensitive: true
strictDate: true
normalizeList: false
```

---

## Understanding the Output Report

The generated Excel workbook will contain a single worksheet titled **`Comparison Report`** structured as follows:

* **`CaseID`**: The unique identifier extracted from the first column of your rows.
* **`Field`**: The column header name where a mismatch was detected (or `[All Fields]` if the case ID itself is entirely missing from Sheet 2).
* **`Sheet1 Value`**: The raw or mapped value recorded in the first file.
* **`Sheet2 Value`**: The raw value recorded in the second file.
* **`Status`**: Explanatory flag (`Missing in Sheet 2` or `Mismatch`).
