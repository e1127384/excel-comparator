# Excel Row Comparator CLI in Go

A robust, high-performance command-line interface (CLI) application written in Go designed to compare two Excel sheets row-by-row based on a primary key (`caseid` in the first column).

It handles missing records, field-by-field discrepancies, flexible date normalization (supporting text/general formats like `06 Aug 2025` and `10-Mar-2027` without time components), list separator normalization (e.g., `,` vs `;`), string equivalence rules (`SIS` and `GI`), data migration mapping transformations, and case-sensitivity toggles, exporting all findings cleanly into an output Excel report.

---

## Features

* **Primary Key Mapping**: Uses the first column as the unique `CaseID`.
* **Missing Record Analysis**: Identifies cases present in Sheet 1 but completely missing in Sheet 2.
* **Field-by-Field Mismatch Detection**: Compares every common field column-by-column.
* **Migration Mapping Rules (`-mapping`)**: Translates old field values to new expected migration values using an external mapping spreadsheet (`FieldName`, `OldValue`, `NewValue`).
* **SIS / GI Equivalence**: Automatically treats the strings `SIS` and `GI` as identical.
* **Date Normalization (`-strict-date`)**: Automatically parses and equates general/text date formats like `06 Aug 2025` and `10-Mar-2027`, while ignoring time components.
* **List Separator Normalization (`-normalize-list`)**: Treats comma-separated and semicolon-separated items (e.g., `a, b` vs `a; b`) identically.
* **Case Sensitivity (`-case-sensitive`)**: Configures whether string comparisons ignore letter casing.
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

## CLI Configuration Flags Reference

| Flag | Default | Description |
| --- | --- | --- |
| `-f1` | `""` | **(Required)** Path to the first Excel file. |
| `-f2` | `""` | **(Required)** Path to the second Excel file. |
| `-s1` | `"Sheet1"` | Sheet name for File 1. |
| `-s2` | `"Sheet1"` | Sheet name for File 2. |
| `-mapping` | `""` | Path to migration mapping Excel file (`FieldName`, `OldValue`, `NewValue`). |
| `-output` | `"comparison_report.xlsx"` | File path for the generated output Excel report. |
| `-case-sensitive` | `false` | Enable or disable case-sensitive text comparison (`true`/`false`). |
| `-strict-date` | `false` | Enable or disable strict raw string date formatting comparison (`true`/`false`). |
| `-normalize-list` | `true` | Treat comma and semicolon lists (e.g., `a, b` vs `a; b`) as equal (`true`/`false`). |

---

## Running the Application

### 1. Basic Comparison (Default Settings)

Performs case-insensitive checks, handles text date variations (like `06 Aug 2025`), strips time components, treats `SIS` and `GI` as equal, and normalizes list punctuation:

```bash
go run main.go -f1 file1.xlsx -f2 file2.xlsx

```

### 2. Running with Migration Mapping Rules

If you are tracking field value migrations or transformations (e.g., changing status codes or mapping values across columns):

```bash
go run main.go -f1 file1.xlsx -f2 file2.xlsx -mapping migration_rules.xlsx

```

*Note: The migration mapping file (`migration_rules.xlsx`) must contain three columns: `FieldName`, `OldValue`, and `NewValue`.*

### 3. Specifying Custom Sheet Names

If your target data lives on custom worksheet tabs:

```bash
go run main.go -f1 file1.xlsx -f2 file2.xlsx -s1 "SheetA" -s2 "SheetB"

```

### 4. Strict Comparison Mode

Enforces strict checks (exact case matching, exact date strings, literal list separators, and strict SIS/GI differentiation):

```bash
go run main.go -f1 file1.xlsx -f2 file2.xlsx -case-sensitive=true -strict-date=true -normalize-list=false

```

---

## Understanding the Output Report

The generated Excel workbook will contain a single worksheet titled **`Comparison Report`** structured as follows:

* **`CaseID`**: The unique identifier extracted from the first column of your rows.
* **`Field`**: The column header name where a mismatch was detected (or `[All Fields]` if the case ID itself is entirely missing from Sheet 2).
* **`Sheet1 Value`**: The raw or mapped value recorded in the first file.
* **`Sheet2 Value`**: The raw value recorded in the second file.
* **`Status`**: Explanatory flag (`Missing in Sheet 2` or `Mismatch`).
