/*
Copyright 2011 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"
	"html"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/revel/revel"
)

func taskList(w http.ResponseWriter, r *http.Request) {
	hostname, _ := os.Hostname()
	drawTemplate(w, "taskList", tmplData{
		"Title": "Tasks on " + hostname,
		"Tasks": GetTasks(),
		"Log":   logBuf.String(),
	})
}

func killTask(w http.ResponseWriter, r *http.Request, t *Task) {
	st := t.Status()
	in := st.Running
	if in == nil {
		http.Error(w, "task not running", 500)
		return
	}
	pid, _ := strconv.Atoi(r.FormValue("pid"))
	if in.Pid() != pid || pid == 0 {
		http.Error(w, "active task pid doesn't match pid parameter", 500)
		return
	}
	t.Stop()
	drawTemplate(w, "killTask", tmplData{
		"Title": "Kill",
		"Task":  t,
		"PID":   pid,
	})
}

func taskView(w http.ResponseWriter, r *http.Request) {
	taskName := r.URL.Path[len("/task/"):]
	t, ok := GetTask(taskName)
	if !ok {
		http.NotFound(w, r)
		return
	}
	mode := r.FormValue("mode")
	switch mode {
	case "kill":
		killTask(w, r, t)
		return
	default:
		http.Error(w, "unknown mode", 400)
		return
	case "":
	}

	data := tmplData{
		"Title": t.Name + " status",
		"Task":  t,
	}

	st := t.Status()
	in := st.Running
	if in != nil {
		data["PID"] = in.Pid()
		data["Output"] = in.Output()
		data["Cmd"] = in.lr
		data["StartTime"] = in.startTime
		data["StartAgo"] = time.Now().Sub(in.startTime)
	}

	// list failures in reverse-chronological order
	{
		f := st.Failures
		r := make([]*TaskInstance, len(f))
		for i := range f {
			r[len(r)-i-1] = f[i]
		}
		data["Failures"] = r
	}

	drawTemplate(w, "viewTask", data)
}

func runWebServer(ln net.Listener) {
	mux := http.NewServeMux()
	// TODO: wrap mux in auth handler, making it available only to
	// TCP connections from localhost and owned by the uid/gid of
	// the running process.
	mux.HandleFunc("/", taskList)
	mux.HandleFunc("/task/", taskView)
	s := &http.Server{
		Handler: mux,
	}
	err := s.Serve(ln)
	if err != nil {
		logger.Fatalf("webserver exiting: %v", err)
	}
}

type tmplData map[string]interface{}

func drawTemplate(w io.Writer, name string, data tmplData) {
	if name != "taskList" {
		hostname, _ := os.Hostname()
		data["RootLink"] = "/"
		data["Hostname"] = hostname
	}
	err := templates[name].ExecuteTemplate(w, "root", data)
	if err != nil {
		logger.Println(err)
	}
}

var templates = make(map[string]*template.Template)

func init() {
	for name, html := range templateHTML {
		t := template.New(name).Funcs(templateFuncs)
		template.Must(t.Parse(html))
		template.Must(t.Parse(rootHTML))
		templates[name] = t
	}
}

const rootHTML = `
{{define "root"}}
<html>
	<head>
		<title>{{.Title}} - runsit</title>
		<style>
		.output {
		   font-family: monospace;
		   font-size: 10pt;
		   border: 2px solid gray;
		   padding: 0.5em;
		   padding-left: 5px;
		   overflow: scroll;
		   max-height: 25em;
		}
		.output div.stderr {
		    box-shadow: -5px 0px 0 #FF9999;
		}
		.output div.system {
		   color: #00c;
		}
                .topbar {
                    font-family: sans;
                    font-size: 10pt;
                }
		</style>
	</head>
	<body>
                {{if .RootLink}}
                    <div id='topbar'>runsit on <a href="{{.RootLink}}">{{.Hostname}}</a>.
                {{end}}
		<h1>{{.Title}}</h1>
		{{template "body" .}}
	</body>
</html>
{{end}}
`

var templateHTML = map[string]string{
	"taskList": `
	{{define "body"}}
		<h2>Running</h2>
		<ul>
		{{range .Tasks}}
			<li><a href='/task/{{.Name}}'>{{.Name}}</a>: {{maybePre .Status.Summary}}</li>
		{{end}}
		</ul>
		<h2>Log</h2>
		<pre>{{.Log}}</pre>
		<tt>{{version}} {{build}}</tt>
	{{end}}
`,
	"killTask": `
	{{define "body"}}
		<p>Killed pid {{.PID}}.</p>
		<p>Back to <a href='/task/{{.Task.Name}}'>{{.Task.Name}} status</a>.</p>
	{{end}}
`,
	"viewTask": `
	{{define "body"}}
		<p>{{maybePre .Task.Status.Summary}}</p>

		{{with .Cmd}}
		{{/* TODO: embolden arg[0] */}}
		<p>command: {{range .Argv}}{{maybeQuote .}} {{end}}</p>
		{{end}}

		{{if .PID}}
		<h2>Running Instance</h2>
                <p>Started {{.StartTime}}, {{.StartAgo}} ago.</p>
		<p>PID={{.PID}} [<a href='/task/{{.Task.Name}}?pid={{.PID}}&mode=kill'>kill</a>]</p>
		{{end}}

		{{with .Output}}{{template "output" .}}{{end}}

		{{with .Failures}}
		<h2>Failures</h2>
		{{range .}}{{template "output" .Output}}{{end}}
		{{end}}

		<script>
		window.addEventListener("load", function() {
		   var d = document.getElementsByClassName("output");
		   for (var i=0; i < d.length; i++) {
		     d[i].scrollTop = d[i].scrollHeight;
		   }
		});
		</script>
        	<tt>{{version}} {{build}}</tt>
	{{end}}
	{{define "output"}}
		<div class='output'>
		{{range .}}
			<div class='{{.Name}}' title='{{.T}}'>{{ansiToHTML .Data}}</div>
		{{end}}
		</div>
	{{end}}
`,
}

var templateFuncs = template.FuncMap{
	"maybeQuote": maybeQuote,
	"maybePre":   maybePre,
	"ansiToHTML": ansiToHTML,
	"build":      build,
	"version":    version,
}

// Base attributes
const (
	Reset int = iota
	Bold
	Faint
	Italic
	Underline
	BlinkSlow
	BlinkRapid
	ReverseVideo
	Concealed
	CrossedOut
)

// text colors
const (
	Black int = iota
	Red
	Green
	Yellow
	Blue
	Magenta
	Cyan
	White
)

func dtoc(d int) string {
	switch d {
	case Black:
		return "Black"
	case Red:
		return "Red"
	case Green:
		return "Green"
	case Yellow:
		return "Orange"
	case Blue:
		return "Blue"
	case Magenta:
		return "Magenta"
	case Cyan:
		return "Cyan"
	case White:
		return "White"
	default:
		return "Black"
	}
}
func ansiToHTML(s string) template.HTML {
	var colorMatcher = regexp.MustCompile(`[\[]+[[\d+;*\d*]+[m]`)

	r := func(s string) string {
		if s == "[0m" {
			return "</span>"
		}
		var colora int
		if n, err := fmt.Sscanf(s, "[%dm", &colora); err != nil {
			return err.Error()
		} else if n == 1 {
			if colora > 39 {
				return fmt.Sprintf("<span style=\"background-color: %s;\">", dtoc(colora%10))
			} else {
				return fmt.Sprintf("<span style=\"color: %s;\">", dtoc(colora%10))
			}
		} else {
			return "<span style=\"color: red; background-color: black;\">"
		}

	}
	bs := colorMatcher.ReplaceAllStringFunc(s, r)
	return template.HTML(bs)
}
func maybeQuote(s string) string {
	if strings.Contains(s, " ") || strings.Contains(s, `"`) {
		return fmt.Sprintf("%q", s)
	}
	return s
}
func version() string {
	if out, err := exec.Command("git", "--git-dir="+os.Getenv("GOPATH")+"/src/github.com/sevki/runsit/.git", "describe", "--dirty", "--always").Output(); err != nil {
		revel.ERROR.Println(err)
		return "deadbeef"
	} else {
		return strings.TrimSpace(string(out))
	}

}
func build() string {
	if out, err := exec.Command("git", "--git-dir="+os.Getenv("GOPATH")+"/src/github.com/sevki/runsit/.git", "rev-parse", "HEAD").Output(); err != nil {
		revel.ERROR.Println(err)
		return "deadbeef"
	} else {
		return strings.TrimSpace(string(out))
	}
}
func maybePre(s string) interface{} {
	if strings.Contains(s, "\n") {
		return template.HTML("<pre>" + html.EscapeString(s) + "</pre>")
	}
	return s
}
