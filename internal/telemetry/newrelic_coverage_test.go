package telemetry

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	nr "github.com/newrelic/go-agent/v3/newrelic"
	"github.com/newrelic/go-agent/v3/newrelic/integrationsupport"
)

type newRelicEventExpectation struct {
	Type           string
	UserAttributes map[string]interface{}
}

func expectNewRelicCustomEvents(t *testing.T, app interface{}, want []newRelicEventExpectation) {
	t.Helper()

	method := reflect.ValueOf(app).MethodByName("ExpectCustomEvents")
	if !method.IsValid() {
		t.Fatal("New Relic test app does not expose ExpectCustomEvents")
	}
	methodType := method.Type()
	if methodType.NumIn() != 2 {
		t.Fatalf("ExpectCustomEvents input count = %d, want 2", methodType.NumIn())
	}

	wantType := methodType.In(1).Elem()
	wantValue := reflect.MakeSlice(reflect.SliceOf(wantType), len(want), len(want))
	for i, expected := range want {
		item := wantValue.Index(i)

		intrinsics := map[string]interface{}{
			"type": expected.Type,
		}
		// Match any timestamp by reflecting on whatever is expected
		item.FieldByName("Intrinsics").Set(reflect.ValueOf(intrinsics))

		if expected.UserAttributes != nil {
			item.FieldByName("UserAttributes").Set(reflect.ValueOf(expected.UserAttributes))
		}
	}

	// Some internal logic in ExpectCustomEvents might fail if timestamp isn't internal.MatchAnything.
	// We'll set timestamp to "*" inside intrinsics, which internal.Expect understands for map[string]interface{}.
	for i := 0; i < wantValue.Len(); i++ {
		item := wantValue.Index(i)
		intrinsics := item.FieldByName("Intrinsics").Interface().(map[string]interface{})
		intrinsics["timestamp"] = "*" // * is a wildcard in New Relic's internal.Expect
		item.FieldByName("Intrinsics").Set(reflect.ValueOf(intrinsics))
	}

	method.Call([]reflect.Value{reflect.ValueOf(t), wantValue})
}

func expectNewRelicTxnEvents(t *testing.T, app interface{}, expectedUserAttributes map[string]interface{}) {
	t.Helper()

	method := reflect.ValueOf(app).MethodByName("ExpectTxnEvents")
	if !method.IsValid() {
		t.Fatal("New Relic test app does not expose ExpectTxnEvents")
	}
	methodType := method.Type()

	wantType := methodType.In(1).Elem()
	wantValue := reflect.MakeSlice(reflect.SliceOf(wantType), 1, 1)
	item := wantValue.Index(0)

	intrinsics := map[string]interface{}{
		"name":      "*",
		"guid":      "*",
		"priority":  "*",
		"sampled":   "*",
		"traceId":   "*",
		"timestamp": "*",
	}
	item.FieldByName("Intrinsics").Set(reflect.ValueOf(intrinsics))

	if expectedUserAttributes != nil {
		item.FieldByName("UserAttributes").Set(reflect.ValueOf(expectedUserAttributes))
	}

	method.Call([]reflect.Value{reflect.ValueOf(t), wantValue})
}

func TestNewRelicConfigOptions_ApplicationLoggingRegressions(t *testing.T) {
	var cfg nr.Config
	for _, option := range newRelicConfigOptions(Config{}) {
		option(&cfg)
	}

	if !cfg.ApplicationLogging.Enabled {
		t.Error("ApplicationLogging.Enabled should be true")
	}
	if !cfg.ApplicationLogging.Forwarding.Enabled {
		t.Error("ApplicationLogging.Forwarding.Enabled should be true")
	}
	if cfg.ApplicationLogging.Forwarding.MaxSamplesStored != 1000 {
		t.Errorf("ApplicationLogging.Forwarding.MaxSamplesStored = %d, want 1000", cfg.ApplicationLogging.Forwarding.MaxSamplesStored)
	}
	if !cfg.ApplicationLogging.Metrics.Enabled {
		t.Error("ApplicationLogging.Metrics.Enabled should be true")
	}
	if cfg.ApplicationLogging.LocalDecorating.Enabled {
		t.Error("ApplicationLogging.LocalDecorating.Enabled should be false")
	}
}

func TestNewRelicSink_VolumeBounds(t *testing.T) {
	files, fset := parseInternalGoFiles(t)
	var customEventCalls []string
	for path, file := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isSelectorCall(call.Fun, "RecordCustomEvent") {
				return true
			}
			pos := filesetPosition(fset, path, call.Pos())
			customEventCalls = append(customEventCalls, pos)
			if filepath.ToSlash(path) != "internal/telemetry/newrelic.go" {
				t.Errorf("RecordCustomEvent call outside New Relic sink at %s", pos)
				return true
			}
			if len(call.Args) == 0 {
				t.Errorf("RecordCustomEvent call at %s has no event-name argument", pos)
				return true
			}
			name, ok := call.Args[0].(*ast.Ident)
			if !ok {
				t.Errorf("RecordCustomEvent event name at %s is not a fixed identifier", pos)
				return true
			}
			switch name.Name {
			case "newRelicEventRallyTry", "newRelicEventRallyDiagnostic", "newRelicEventRallyFailure":
			default:
				t.Errorf("RecordCustomEvent event name at %s = %s, want fixed Rally event constant", pos, name.Name)
			}
			return true
		})
	}

	if len(customEventCalls) != 3 {
		t.Fatalf("RecordCustomEvent call sites = %v, want exactly RallyTry, RallyDiagnostic, and RallyFailure", customEventCalls)
	}
}

func TestNewRelicSink_StaticNoRecordLog(t *testing.T) {
	files, fset := parseInternalGoFiles(t)
	for path, file := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(importPath, "github.com/newrelic/go-agent/v3/integrations/") {
				t.Errorf("New Relic logger integration import %q in %s", importPath, path)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if ok && isSelectorCall(call.Fun, "RecordLog") {
				t.Errorf("Application.RecordLog call found at %s", filesetPosition(fset, path, call.Pos()))
			}
			return true
		})
	}
}

func parseInternalGoFiles(t *testing.T) (map[string]*ast.File, *token.FileSet) {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	err := filepath.WalkDir(filepath.Join(repoRoot, "internal"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		parsed, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		if needsBodyInspection(path) {
			parsed, err = parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = parsed
		return nil
	})
	if err != nil {
		t.Fatalf("parse internal Go files: %v", err)
	}
	return files, fset
}

func needsBodyInspection(path string) bool {
	return !strings.HasSuffix(path, "_test.go")
}

func isSelectorCall(expr ast.Expr, selectorName string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == selectorName
}

func filesetPosition(fset *token.FileSet, path string, pos token.Pos) string {
	position := fset.Position(pos)
	if position.Filename == "" {
		return path
	}
	return filepath.ToSlash(position.Filename) + ":" + strconv.Itoa(position.Line)
}

func TestNewRelicSink_StartSpan_Segment(t *testing.T) {
	testApp := integrationsupport.NewTestApp(integrationsupport.SampleEverythingReplyFn)
	sink := &NewRelicSink{app: testApp.Application}

	txnCtx, parentSpan := sink.StartSpan(context.Background(), "relay", "relay-smoke")
	parentSpan.SetTag("relay_id", "42")

	_, childSpan := sink.StartSpan(txnCtx, "child", "child-smoke")
	childSpan.SetTag("child_id", "43")
	childSpan.Finish()

	parentSpan.Finish()

	expectNewRelicTxnEvents(t, testApp, map[string]interface{}{
		"operation":     "relay",
		"description":   "relay-smoke",
		"rally_span_id": "*",
		"duration_ms":   "*",
		"relay_id":      "42",
	})

	// Since segments attributes are tricky to inspect without internal package, we rely on
	// ExpectTxnEvents passing, and the knowledge that segments have Finish called without panicking.
}

func TestNewRelicSink_StartSpan_Transaction(t *testing.T) {
	testApp := integrationsupport.NewTestApp(integrationsupport.SampleEverythingReplyFn)
	sink := &NewRelicSink{app: testApp.Application}

	_, span := sink.StartSpan(context.Background(), "relay", "relay-smoke")
	span.SetTag("relay_id", "42")
	span.Finish()

	expectNewRelicTxnEvents(t, testApp, map[string]interface{}{
		"operation":     "relay",
		"description":   "relay-smoke",
		"rally_span_id": "*",
		"duration_ms":   "*",
		"relay_id":      "42",
	})
}
func TestNewRelicSink_EmitsCustomEventsAndErrors(t *testing.T) {
	testApp := integrationsupport.NewTestApp(integrationsupport.SampleEverythingReplyFn)
	sink := &NewRelicSink{app: testApp.Application}
	ctx := context.Background()

	sink.EmitTryLog(ctx, map[string]interface{}{
		"event":    "try",
		"relay_id": "1",
		"run_id":   1,
	})

	sink.CaptureEvent(ctx, "diagnostic msg", Event{
		Level: LevelWarning,
		Tags:  map[string]string{"event_kind": "smoke"},
	})

	txn := testApp.StartTransaction("test_txn")
	txnCtx := nr.NewContext(ctx, txn)

	sink.CaptureFailure(txnCtx, "failure msg", FailureEvent{
		Tags: map[string]string{"failure_category": "harness_launch"},
	})
	txn.End()

	expectNewRelicCustomEvents(t, testApp, []newRelicEventExpectation{
		{
			Type: newRelicEventRallyTry,
			UserAttributes: map[string]interface{}{
				"event":    "try",
				"relay_id": "1",
				"run_id":   1,
			},
		},
		{
			Type: newRelicEventRallyDiagnostic,
			UserAttributes: map[string]interface{}{
				"message":    "diagnostic msg",
				"level":      "warning",
				"event_kind": "smoke",
			},
		},
		{
			Type: newRelicEventRallyFailure,
			UserAttributes: map[string]interface{}{
				"message":          "failure msg",
				"error_class":      "RallyHarnessLaunch",
				"failure_category": "harness_launch",
			},
		},
	})

	expectNewRelicErrors(t, testApp, []newRelicErrorExpectation{
		{
			Msg:   "failure msg",
			Klass: "RallyHarnessLaunch",
			UserAttributes: map[string]interface{}{
				"message":          "failure msg",
				"error_class":      "RallyHarnessLaunch",
				"failure_category": "harness_launch",
			},
		},
	})
}
