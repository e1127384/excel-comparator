Excel Row Comparator CLI in Go
A lightweight, high-performance command-line interface (CLI) application written in Go to compare two Excel sheets row-by-row based on a primary key (caseid in the first column).

It handles missing records, field-by-field discrepancies, flexible date normalization (supporting formats like 10-Mar-2027 without time components), and case-sensitivity configuration, exporting all findings cleanly into an output Excel report.

Prerequisites
Make sure you have the following installed on your machine:

Go (version 1.18 or higher recommended)

Project Setup & Installation
Clone or create your project directory:

Bash


mkdir excel-comparator
cd excel-comparator
Initialize the Go module:

Bash


mod_name="excel-comparator" # or your preferred module name
go mod init $mod_name
Install the required Excel processing library (excelize):

Bash


go get github.com/xuri/excelize/v2
Add the Code:
Save the application source code into a file named main.go in your project root directory.

Configuration & Usage
CLI Flags Reference
Flag	Default	Description
-f1	""	(Required) Path to the first Excel file.
-f2	""	(Required) Path to the second Excel file.
-s1	"Sheet1"	Sheet name for File 1.
-s2	"Sheet1"	Sheet name for File 2.
-output	"comparison_report.xlsx"	File path for the generated output Excel report.
-case-sensitive	false	Enable or disable case-sensitive text comparison (true/false).
-strict-date	false	Enable or disable strict raw string date formatting comparison (true/false).

Running the Application
1. Basic Comparison (Default Settings)
Performs case-insensitive text checks and normalizes dates (supporting formats like 10-Mar-2027 vs 2027-03-10 automatically, stripping time values):

Bash


go run main.go -f1 file1.xlsx -f2 file2.xlsx
2. Specifying Custom Sheet Names
If your tables reside on specific sheet names:

Bash


go run main.go -f1 file1.xlsx -f2 file2.xlsx -s1 "Data_v1" -s2 "Data_v2"
3. Strict Comparison (Case-Sensitive & Strict Dates)
Enforces exact matching rules for text casing and strict string-level date formats:

Bash


go run main.go -f1 file1.xlsx -f2 file2.xlsx -case-sensitive=true -strict-date=true
4. Customizing the Output Report Path
Bash


go run main.go -f1 file1.xlsx -f2 file2.xlsx -output ./reports/audit_result.xlsx
Understanding the Output Report
The generated Excel report will contain a single worksheet titled Comparison Report with the following columns:

CaseID: The unique identifier extracted from the first column of your rows.

Field: The column header name where a mismatch or difference was detected (or [All Fields] if the case ID itself is entirely missing from Sheet 2).

Sheet1 Value: The value recorded in the first file.

Sheet2 Value: The value recorded in the second file.

Status: Explanatory flag (Missing in Sheet 2 or Mismatch).
