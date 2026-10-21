{{if .Code -}}
A smaller model tried to write this symbol and did not succeed. Its last attempt:

```
{{.Code}}
```
{{if .Problems}}
Remaining problems:
{{range .Problems}}- {{.}}
{{end}}{{end}}
{{- end}}
Write the symbol yourself, correctly and completely, following the task above. Answer with the
JSON object only.
