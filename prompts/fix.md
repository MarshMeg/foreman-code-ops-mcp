Your code was inserted into {{.File}}, but the checks failed:

{{range .Diagnostics}}- {{.}}
{{end}}
{{- if .Code}}
The file region now contains (after import fixing and formatting):

```
{{.Code}}
```
{{end}}
Fix the problems and answer with the complete corrected symbol as the same JSON object.
Change only what is needed to make the checks pass.
