package ui

import (
	"strings"
	"testing"

	"github.com/dop251/goja"
)

func bootVM(t *testing.T, resolve bool) *goja.Runtime {
	t.Helper()
	return bootVMSettling(t, settleFor(resolve))
}

func settleFor(resolve bool) string {
	if !resolve {
		return "reject(new Error('denied'))"
	}
	return "resolve({structuredContent:{posts:[{id:'p1',title:'Shipped',status:'published'}]}})"
}

func bootVMSettling(t *testing.T, settle string) *goja.Runtime {
	t.Helper()
	vm := newJSVM(t)
	stub, err := fixtures.ReadFile("testdata/dom_stub.js")
	if err != nil {
		t.Fatalf("read dom stub: %v", err)
	}
	if _, err := vm.RunString(string(stub)); err != nil {
		t.Fatalf("eval dom stub: %v", err)
	}
	if _, err := vm.RunString(`
		globalThis.recorded = {calls: []};
		globalThis.__extApps = null;
		globalThis.fakeLoader = function () {
			return {
				App: function (info, caps) {
					globalThis.recorded.caps = caps;
					globalThis.__app = this;
					this.connect = function () { return Promise.resolve(); };
					this.getHostContext = function () { return { toolInfo: { tool: { name: "blog_list" } } }; };
					this.callServerTool = function (req) {
						globalThis.recorded.calls.push(req);
						return new Promise(function (resolve, reject) { ` + settle + `; });
					};
				},
				applyDocumentTheme: function () {},
				applyHostStyleVariables: function () {},
				applyHostFonts: function () {},
			};
		};
	`); err != nil {
		t.Fatalf("install fake: %v", err)
	}
	if _, err := vm.RunString(mustRead(t, "src/bridge.js")); err != nil {
		t.Fatalf("eval bridge.js: %v", err)
	}
	src := strings.Replace(mustRead(t, "src/boot.js"),
		"function () { return globalThis.__extApps; }", "globalThis.fakeLoader", 1)
	if _, err := vm.RunString(src); err != nil {
		t.Fatalf("eval boot.js: %v", err)
	}
	// SECURITY: the served bundle exports no dispatch seam; the test installs one after evaluation.
	if _, err := vm.RunString(`
		globalThis.__dispatchAction = dispatchAction;
		globalThis.__toolInput = function (p, tool) { hostHandlers.onToolInput(p, tool); };
	`); err != nil {
		t.Fatalf("install seam: %v", err)
	}
	return vm
}

func hostNamed(t *testing.T, vm *goja.Runtime, name string) {
	t.Helper()
	if _, err := vm.RunString(`__app.getHostContext = function () { return { toolInfo: { tool: { name: ` + jsQuote(name) + ` } } }; };`); err != nil {
		t.Fatalf("set host context: %v", err)
	}
}

func lastArgs(t *testing.T, vm *goja.Runtime) string {
	t.Helper()
	return jsString(t, vm, `JSON.stringify(recorded.calls[recorded.calls.length - 1].arguments)`)
}

func TestBootMountsTheAppElement(t *testing.T) {
	vm := bootVM(t, true)
	if got := jsString(t, vm, `__root.children[0].tagName`); got != "opensheet-app" {
		t.Errorf("root holds %q, want opensheet-app", got)
	}
	if got := jsString(t, vm, `typeof __appEl.onaction`); got != "function" {
		t.Errorf("boot.js did not wire the app element's action channel, got %q", got)
	}
}

func TestBootMergesToolInputAndFormOverDeclaredArgs(t *testing.T) {
	vm := bootVM(t, true)
	if _, err := vm.RunString(`
		__toolInput({ arguments: { target: { org: "acme" }, projectId: "prj_1", status: "draft" } }, "blog_list");
		current = { actions: { refresh: { tool: "blog_list", args: { status: "published" } } } };
		const form = { children: [ { attrs: { name: "projectId" }, value: "prj_2", getAttribute(k){ return this.attrs[k] || null; } } ],
		               querySelectorAll(){ return this.children; } };
		__dispatchAction("refresh", __mkEl({}), form);
	`); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	got := jsString(t, vm, `JSON.stringify(recorded.calls[0])`)
	const want = `{"name":"blog_list","arguments":{"target":{"org":"acme"},"projectId":"prj_2","status":"published"}}`
	if got != want {
		t.Errorf("callServerTool got\n %s\nwant\n %s", got, want)
	}
}

// TestBootFormFieldCannotOverrideADeclaredArgument: a.args merges last, so a field named like a declared argument cannot repoint the write at another row.
func TestBootFormFieldCannotOverrideADeclaredArgument(t *testing.T) {
	vm := bootVM(t, true)
	if _, err := vm.RunString(`
		current = { actions: { publish: { tool: "blog_publish", args: { postId: "declared" } } } };
		__dispatchAction(
			"publish",
			__mkEl({}),
			{
				querySelectorAll: function (sel) {
					if (sel !== "[name]") return [];
					return [__mkEl({ name: "postId" })].map(function (el) { el.value = "someone-elses-post"; return el; });
				},
			}
		);
	`); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := lastArgs(t, vm); !strings.Contains(got, `"postId":"declared"`) {
		t.Errorf("a form field overrode the declared argument: %s", got)
	}
}

func TestBootIgnoresToolInputFromADifferentTool(t *testing.T) {
	vm := bootVM(t, true)
	if _, err := vm.RunString(`
		__toolInput({ arguments: { postId: "from-another-tool", status: "stale" } }, "blog_list");
		current = { actions: { publish: { tool: "blog_publish", args: {} } } };
		__dispatchAction("publish", __mkEl({}), null);
	`); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := lastArgs(t, vm); strings.Contains(got, "stale") {
		t.Errorf("args = %s; another tool's captured input must not leak into a write", got)
	}
}

// TestBootSubjectReadsTheViewsOwnAnswersNotTheHostAnnouncement: the tool-input notification carries no tool name, so a late host announcement must not become the view's own state.
func TestBootSubjectReadsTheViewsOwnAnswersNotTheHostAnnouncement(t *testing.T) {
	vm := bootVM(t, true)
	hostNamed(t, vm, "blog_publish")
	if _, err := vm.RunString(`
		answers = { tool: "blog_publish", args: { postId: "the-view-s-own" } };
		__app.ontoolinput({ arguments: { postId: "from-the-host" } });
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if got := jsString(t, vm, `JSON.stringify(capturedArgs())`); strings.Contains(got, "from-the-host") {
		t.Errorf("capturedArgs surfaced the host announcement instead of the view's answers: %s", got)
	}
	if _, err := vm.RunString(`
		current = { actions: { publish: { tool: "blog_publish", args: {} } } };
		__dispatchAction("publish", __mkEl({}), null);
	`); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := lastArgs(t, vm); !strings.Contains(got, `"postId":"the-view-s-own"`) {
		t.Errorf("the write used the host announcement instead of the view's own answers: %s", got)
	}
}

// TestBootWriteCannotFireTwiceWhileACallIsInflight re-declares the action between dispatches so the inflight latch is the only thing left to block the second write.
func TestBootWriteCannotFireTwiceWhileACallIsInflight(t *testing.T) {
	vm := bootVM(t, true)
	if _, err := vm.RunString(`
		const decl = { tool: "blog_publish", args: { postId: "p1" } };
		current = { actions: { publish: decl } };
		const btn = __mkEl({});
		__dispatchAction("publish", btn, null);
		current.actions.publish = decl;
		__dispatchAction("publish", btn, null);
	`); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := jsString(t, vm, `String(recorded.calls.length)`); got != "1" {
		t.Errorf("callServerTool fired %s times, want 1 — a second click while a call is inflight must not write again", got)
	}
}

// TestBootWriteCannotFireTwiceOnceTheActionIsConsumed clears the inflight latch between dispatches so consuming the declared action is the only thing left to block the second write.
func TestBootWriteCannotFireTwiceOnceTheActionIsConsumed(t *testing.T) {
	vm := bootVM(t, true)
	if _, err := vm.RunString(`
		current = { actions: { publish: { tool: "blog_publish", args: { postId: "p1" } } } };
		const btn = __mkEl({});
		__dispatchAction("publish", btn, null);
		inflight = null;
		__dispatchAction("publish", btn, null);
	`); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := jsString(t, vm, `String(recorded.calls.length)`); got != "1" {
		t.Errorf("callServerTool fired %s times, want 1 — a consumed action must not write again", got)
	}
}

// TestBootFormFieldCannotPoisonALaterCall: a field name is attacker-controlled whenever a view renders one from tool output, so a dotted path must never reach Object.prototype.
func TestBootFormFieldCannotPoisonALaterCall(t *testing.T) {
	for _, tc := range []struct{ name, field, value string }{
		{"proto segment", "__proto__.postId", `"attacker-owned"`},
		{"constructor prototype segment", "constructor.prototype.postId", `"attacker-owned"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm := bootVM(t, true)
			if _, err := vm.RunString(`
				current = { actions: { forge: { tool: "blog_list", args: {} } } };
				__dispatchAction("forge", __mkEl({}), {
					querySelectorAll: function (sel) {
						if (sel !== "[name]") return [];
						const el = __mkEl({ name: ` + jsQuote(tc.field) + ` });
						el.value = ` + tc.value + `;
						return [el];
					},
				});
				inflight = null;
				current = { actions: { publish: { tool: "blog_publish", args: {} } } };
				__dispatchAction("publish", __mkEl({}), null);
			`); err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if got := jsString(t, vm, `String(recorded.calls.length)`); got != "2" {
				t.Fatalf("recorded %s calls, want 2", got)
			}
			if got := jsString(t, vm, `JSON.stringify(recorded.calls)`); strings.Contains(got, "attacker-owned") || strings.Contains(got, "postId") {
				t.Errorf("a forged form field reached a tool call's arguments: %s", got)
			}
			if got := jsString(t, vm, `String(({}).postId)`); got != "undefined" {
				t.Errorf("a forged form field wrote %q onto Object.prototype", got)
			}
		})
	}
}

// TestBootToolInputCannotPoisonALaterCall: JSON.parse mints a real own "__proto__" key, so a deep merge that followed it would write onto Object.prototype.
func TestBootToolInputCannotPoisonALaterCall(t *testing.T) {
	vm := bootVM(t, true)
	if _, err := vm.RunString(`
		__toolInput({ arguments: JSON.parse('{"__proto__":{"postId":"attacker-owned"}}') }, "blog_list");
		current = { actions: { list: { tool: "blog_list", args: {} } } };
		__dispatchAction("list", __mkEl({}), null);
		inflight = null;
		current = { actions: { publish: { tool: "blog_publish", args: {} } } };
		__dispatchAction("publish", __mkEl({}), null);
	`); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := jsString(t, vm, `String(recorded.calls.length)`); got != "2" {
		t.Fatalf("recorded %s calls, want 2", got)
	}
	if got := jsString(t, vm, `JSON.stringify(recorded.calls)`); strings.Contains(got, "attacker-owned") || strings.Contains(got, "postId") {
		t.Errorf("a forged tool-input key reached a tool call's arguments: %s", got)
	}
	if got := jsString(t, vm, `String(({}).postId)`); got != "undefined" {
		t.Errorf("a forged tool-input key wrote %q onto Object.prototype", got)
	}
}

// TestBootIgnoresAnActionNamedLikeAnInheritedMember: every action map the dispatcher reads is null-prototype, so "toString" resolves to nothing rather than to an inherited member.
func TestBootIgnoresAnActionNamedLikeAnInheritedMember(t *testing.T) {
	for _, tc := range []struct {
		name    string
		resolve bool
		seed    string
	}{
		{"a rendered view", true, `current = renderTool("blog_list", { posts: [] });`},
		{"a result with no tool name", true, `paint("", {});`},
		{"a failed result", true, `paintResult("blog_list", { isError: true });`},
		{"a cancelled call", true, `paintCancelled("nope");`},
		{"a rejected call", false, `current = { actions: { go: { tool: "blog_list", args: {} } } }; __dispatchAction("go", __mkEl({}), null);`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm := bootVMSettling(t, settleFor(tc.resolve))
			if _, err := vm.RunString(tc.seed); err != nil {
				t.Fatalf("seed: %v", err)
			}
			before := jsString(t, vm, `String(recorded.calls.length)`)
			for _, id := range []string{"toString", "valueOf", "constructor", "hasOwnProperty"} {
				if _, err := vm.RunString(`inflight = null; __dispatchAction(` + jsQuote(id) + `, __mkEl({}), null);`); err != nil {
					t.Fatalf("dispatch %s: %v", id, err)
				}
			}
			if got := jsString(t, vm, `String(recorded.calls.length)`); got != before {
				t.Errorf("an action named like an inherited member fired a call: %s calls, want %s", got, before)
			}
		})
	}
}

func TestBootPaintsFromTheCallToolPromise(t *testing.T) {
	vm := bootVM(t, true)
	if _, err := vm.RunString(`
		current = { actions: { go: { tool: "blog_list", args: {} } } };
		__dispatchAction("go", __mkEl({}), null);
	`); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := jsString(t, vm, `__appEl.status`); got != "view" {
		t.Errorf("app status = %q, want view", got)
	}
	if got := jsString(t, vm, `JSON.stringify(__appEl.view.model)`); !strings.Contains(got, "Shipped") {
		t.Errorf("panel did not repaint from the callTool promise:\n%s", got)
	}
}

func TestBootShowsARejectedState(t *testing.T) {
	vm := bootVM(t, false)
	if _, err := vm.RunString(`
		current = { actions: { go: { tool: "blog_list", args: {} } } };
		__dispatchAction("go", __mkEl({}), null);
	`); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := jsString(t, vm, `__appEl.status`); got != "error" {
		t.Errorf("a denied or failed call must show a visible error state, got %q", got)
	}
}

func TestBootIgnoresAnActionItNeverDeclared(t *testing.T) {
	vm := bootVM(t, true)
	if _, err := vm.RunString(`
		current = { actions: { publish: { tool: "blog_publish", args: { postId: "p1" } } } };
		__dispatchAction("forged", __mkEl({}), null);
	`); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := jsString(t, vm, `String(recorded.calls.length)`); got != "0" {
		t.Errorf("an undeclared action id fired %s calls, want 0", got)
	}
}

// TestBootSetInRejectsAWholeNameProto: "__proto__" as a whole field name swaps the form object's prototype, which no later own-key enumeration would surface.
func TestBootSetInRejectsAWholeNameProto(t *testing.T) {
	vm := bootVM(t, true)
	if _, err := vm.RunString(`
		globalThis.__out = {};
		setIn(__out, "__proto__", { postId: "attacker-owned" });
	`); err != nil {
		t.Fatalf("setIn: %v", err)
	}
	if got := jsString(t, vm, `String(Object.getPrototypeOf(__out) === Object.prototype)`); got != "true" {
		t.Error("setIn replaced the target's prototype")
	}
	if got := jsString(t, vm, `String(__out.postId)`); got != "undefined" {
		t.Errorf("setIn leaked %q onto the target through its prototype", got)
	}
}

// TestBootInheritedKeysNeverReachToolArguments: mergeDeep enumerates own keys only, so a prototype polluted through any other path cannot add arguments to a later tool call.
func TestBootInheritedKeysNeverReachToolArguments(t *testing.T) {
	vm := bootVM(t, true)
	if _, err := vm.RunString(`
		Object.defineProperty(Object.prototype, "postId", {
			value: "inherited", enumerable: true, configurable: true, writable: true,
		});
		try {
			__toolInput({ arguments: {} }, "blog_publish");
			current = { actions: { publish: { tool: "blog_publish", args: {} } } };
			__dispatchAction("publish", __mkEl({}), null);
		} finally {
			delete Object.prototype.postId;
		}
	`); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := lastArgs(t, vm); strings.Contains(got, "inherited") || strings.Contains(got, "postId") {
		t.Errorf("an inherited key reached a tool call's arguments: %s", got)
	}
}
