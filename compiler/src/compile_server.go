package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"llcontext/src/backend"
	"llcontext/src/frontendir"
	"llcontext/src/grammar"
	"llcontext/src/interpreter"
	"llcontext/src/unparse"
)

type compileServerRequest struct {
	Mode     string `json:"mode"`
	Filter   string `json:"filter,omitempty"`
	Filename string `json:"filename,omitempty"`
	Source   string `json:"source,omitempty"`
	IR       string `json:"ir,omitempty"`
}

type compileServerResponse struct {
	OK        bool   `json:"ok"`
	Output    string `json:"output,omitempty"`
	IR        string `json:"ir,omitempty"`
	Value     string `json:"value,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
	Error     string `json:"error,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

func serveCompileServer(addr string, stdout io.Writer, stderr io.Writer) error {
	if strings.TrimSpace(addr) == "" {
		addr = "127.0.0.1:8080"
	}
	if stderr != nil {
		fmt.Fprintf(stderr, "llcontext compile server listening on http://%s\n", addr)
	}
	return http.ListenAndServe(addr, newCompileServerMux(stdout, stderr))
}

func newCompileServerMux(stdout io.Writer, stderr io.Writer) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("/api/v1/compile", func(w http.ResponseWriter, r *http.Request) {
		handleCompileServerAPI(w, r)
	})
	return mux
}

func handleCompileServerAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(compileServerResponse{OK: false, Error: "only POST is supported"})
		return
	}
	var req compileServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(compileServerResponse{OK: false, Error: err.Error()})
		return
	}
	resp, status := executeCompileServerRequest(req)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

func executeCompileServerRequest(req compileServerRequest) (compileServerResponse, int) {
	var stderr bytes.Buffer
	program, err := loadedProgramFromRequest(req)
	if err != nil {
		return compileServerResponse{OK: false, Error: err.Error()}, http.StatusBadRequest
	}
	mode := normalizeEmitMode(req.Mode)
	if mode == "" {
		mode = emitAST
	}
	response := compileServerResponse{OK: true}
	switch mode {
	case emitAST:
		file, ok := parseLoadedProgram(program, &stderr)
		if !ok {
			return compileServerResponse{OK: false, Error: "frontend parse failed", Stderr: strings.TrimSpace(stderr.String())}, http.StatusBadRequest
		}
		emitSemanticWarningsIfNoErrors(file, &stderr)
		var out bytes.Buffer
		printFile(&out, file)
		response.Output = out.String()
	case emitLowered:
		file, ok := parseLoadedProgram(program, &stderr)
		if !ok {
			return compileServerResponse{OK: false, Error: "frontend parse failed", Stderr: strings.TrimSpace(stderr.String())}, http.StatusBadRequest
		}
		response.Output = unparse.FormatFile(grammar.LowerFile(file))
	case emitSemantic:
		_, result, ok := analyzeLoadedProgram(program, &stderr)
		if !ok {
			return compileServerResponse{OK: false, Error: "frontend analysis failed", Stderr: strings.TrimSpace(stderr.String())}, http.StatusBadRequest
		}
		response.Output = generateSemanticReport(result)
	case emitFacts:
		_, result, ok := analyzeLoadedProgram(program, &stderr)
		if !ok {
			return compileServerResponse{OK: false, Error: "frontend analysis failed", Stderr: strings.TrimSpace(stderr.String())}, http.StatusBadRequest
		}
		facts, err := generateFactTraceReport(result, req.Filter)
		if err != nil {
			return compileServerResponse{OK: false, Error: err.Error(), ErrorCode: "fact_trace_filter", Stderr: strings.TrimSpace(stderr.String())}, http.StatusBadRequest
		}
		response.Output = facts
	case emitFmt:
		file, ok := parseLoadedProgram(program, &stderr)
		if !ok {
			return compileServerResponse{OK: false, Error: "frontend parse failed", Stderr: strings.TrimSpace(stderr.String())}, http.StatusBadRequest
		}
		emitSemanticWarningsIfNoErrors(file, &stderr)
		response.Output = unparse.FormatFile(file)
	case emitDoc:
		file, ok := parseLoadedProgram(program, &stderr)
		if !ok {
			return compileServerResponse{OK: false, Error: "frontend parse failed", Stderr: strings.TrimSpace(stderr.String())}, http.StatusBadRequest
		}
		emitSemanticWarningsIfNoErrors(file, &stderr)
		response.Output = generateReferenceDoc(program.filename, file)
	case emitIR:
		file, _, ok := analyzeLoadedProgram(program, &stderr)
		if !ok {
			return compileServerResponse{OK: false, Error: "frontend analysis failed", Stderr: strings.TrimSpace(stderr.String())}, http.StatusBadRequest
		}
		encoded, err := frontendir.Encode(buildFrontendIRBundle(program, file))
		if err != nil {
			return compileServerResponse{OK: false, Error: err.Error(), Stderr: strings.TrimSpace(stderr.String())}, http.StatusInternalServerError
		}
		response.IR = base64.StdEncoding.EncodeToString(encoded)
	case emitLLVM:
		_, result, ok := analyzeLoadedProgram(program, &stderr)
		if !ok {
			return compileServerResponse{OK: false, Error: "frontend analysis failed", Stderr: strings.TrimSpace(stderr.String())}, http.StatusBadRequest
		}
		output, err := backend.GenerateLLVMIRWithOptAndPackedLoweringProfile(result, backend.OptimizationLevel0, backend.DefaultPackedLoweringProfile())
		if err != nil {
			return compileServerResponse{OK: false, Error: err.Error(), Stderr: strings.TrimSpace(stderr.String())}, http.StatusInternalServerError
		}
		response.Output = output
	case emitInterpret:
		_, result, ok := analyzeLoadedProgram(program, &stderr)
		if !ok {
			return compileServerResponse{OK: false, Error: "frontend analysis failed", Stderr: strings.TrimSpace(stderr.String())}, http.StatusBadRequest
		}
		execResult, err := interpreter.Execute(result, interpreter.Options{})
		if err != nil {
			return compileServerResponse{OK: false, Error: err.Error(), Stderr: strings.TrimSpace(stderr.String())}, http.StatusBadRequest
		}
		response.Output = execResult.Stdout
		if !execResult.Return.IsVoid() {
			response.Value = execResult.Return.String()
		}
	default:
		return compileServerResponse{OK: false, Error: fmt.Sprintf("unsupported mode %q", req.Mode)}, http.StatusBadRequest
	}
	if trimmed := strings.TrimSpace(stderr.String()); trimmed != "" {
		response.Stderr = trimmed
	}
	return response, http.StatusOK
}

func loadedProgramFromRequest(req compileServerRequest) (*loadedProgram, error) {
	if strings.TrimSpace(req.Source) == "" && strings.TrimSpace(req.IR) == "" {
		return nil, fmt.Errorf("request must include source or ir")
	}
	if strings.TrimSpace(req.Source) != "" && strings.TrimSpace(req.IR) != "" {
		return nil, fmt.Errorf("request must include source or ir, not both")
	}
	filename := strings.TrimSpace(req.Filename)
	if strings.TrimSpace(req.IR) != "" {
		data, err := base64.StdEncoding.DecodeString(req.IR)
		if err != nil {
			return nil, fmt.Errorf("invalid base64 ir payload: %w", err)
		}
		bundle, err := frontendir.Decode(data)
		if err != nil {
			return nil, err
		}
		if filename == "" {
			filename = bundle.SourceFilename
		}
		if filename == "" {
			filename = "request" + frontendIRExtension
		}
		return &loadedProgram{filename: filename, source: append([]byte(nil), bundle.ResolvedSource...), file: bundle.File}, nil
	}
	if filename == "" {
		filename = "request.llcontext"
	}
	return &loadedProgram{filename: filename, source: []byte(req.Source)}, nil
}
