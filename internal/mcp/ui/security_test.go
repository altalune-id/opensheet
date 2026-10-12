package ui

import (
	"strings"
	"testing"
)

// TestForgedRawMarkerCannotBypassEscaping: unsafeHTML is Lit's only escaping opt-out, so a duck-typed {__raw:…} from a tool result must fall back to the declared placeholder.
func TestForgedRawMarkerCannotBypassEscaping(t *testing.T) {
	vm := newJSVM(t)
	const forged = `{"__raw":"<img src=x onerror=alert(1)>"}`
	for _, tc := range []struct{ name, data, field, want string }{
		{"title", `{posts:[{id:"a",status:"draft",title:` + forged + `}]}`, "title", "Untitled"},
		{"category name", `{posts:[{id:"a",status:"draft",category:{name:` + forged + `}}]}`, "category", "—"},
		{"tag name", `{posts:[{id:"a",status:"draft",tags:[{name:` + forged + `}]}]}`, "tags", ""},
		{"id", `{posts:[{id:` + forged + `,status:"draft"}]}`, "id", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := `renderTool("blog_list", ` + tc.data + `).model.rows[0]`
			if got := jsString(t, vm, row+"."+tc.field); got != tc.want {
				t.Errorf("%s = %q, want the declared fallback %q", tc.field, got, tc.want)
			}
			got := jsString(t, vm, `JSON.stringify(`+row+`)`)
			for _, bad := range []string{"__raw", "<img src=x", "[object Object]"} {
				if strings.Contains(got, bad) {
					t.Errorf("a forged __raw marker left %q in the view model:\n%s", bad, got)
				}
			}
		})
	}
}

// TestViewModelEmitsOnlyStringsThisBundleMinted: anything else reaching the render layer would carry the tool result's own shape past the model.
func TestViewModelEmitsOnlyStringsThisBundleMinted(t *testing.T) {
	vm := newJSVM(t)
	got := jsString(t, vm, `(function () {
		const m = renderTool("blog_list", `+fixtureJSON(t, "blog_list.json")+`).model;
		const bad = [];
		m.kpis.concat(m.rows).forEach(function (o) {
			for (const k in o) { if (typeof o[k] !== "string") bad.push(k + "=" + typeof o[k]); }
		});
		return bad.join(",");
	})()`)
	if got != "" {
		t.Errorf("the view model carries non-string leaves: %s", got)
	}
}

// TestNoHTMLSinkOrUnsafeDirectiveInTheBundle: Lit's contract holds only while nothing opts out of it.
func TestNoHTMLSinkOrUnsafeDirectiveInTheBundle(t *testing.T) {
	for _, p := range scriptParts {
		src := mustRead(t, p)
		for _, bad := range []string{"unsafeHTML", "unsafeSVG", "unsafeStatic", "unsafeCSS", "innerHTML", "outerHTML", "insertAdjacentHTML", "document.write"} {
			if strings.Contains(src, bad) {
				t.Errorf("%s uses %s; Lit escapes text-position interpolations only while nothing opts out", p, bad)
			}
		}
	}
	doc := Document()
	for _, bad := range []string{"unsafeHTML:", "unsafeSVG:", "unsafeStatic:"} {
		if strings.Contains(doc, bad) {
			t.Errorf("the vendored bundle exposes %s to the page", bad)
		}
	}
}

// TestBadgeColourNeverEmitsAnUnvalidatedValue: the value lands in a raw style attribute, where Lit does not sanitize and HTML escaping is insufficient.
func TestBadgeColourNeverEmitsAnUnvalidatedValue(t *testing.T) {
	vm := newJSVM(t)
	for _, token := range []string{
		`#000" onmouseover="alert(1)`,
		`#000;position:fixed;inset:0;opacity:0`,
		`javascript:alert(1)`,
		`url(https://evil/beacon)`,
		`published;background:url(https://evil/b)`,
	} {
		got := jsString(t, vm, `badgeColour(`+jsQuote(token)+`)`)
		if strings.ContainsAny(got, `";:()`) && !strings.HasPrefix(got, "var(--color-badge-") {
			t.Errorf("badgeColour(%q) = %q — only an allow-listed hex or badge token may reach an attribute", token, got)
		}
		if strings.Contains(got, "evil") || strings.Contains(got, "onmouseover") || strings.Contains(got, "position:fixed") {
			t.Errorf("badgeColour(%q) = %q leaked the attacker's payload", token, got)
		}
	}
	if got := jsString(t, vm, `badgeColour("#a1b2c3")`); got != "#a1b2c3" {
		t.Errorf("a valid hex must pass through, got %q", got)
	}
	if got := jsString(t, vm, `badgeColour("published")`); !strings.HasPrefix(got, "var(--color-badge-published,") {
		t.Errorf("a valid token must resolve to its custom property, got %q", got)
	}
}

func TestStyleAttributeCannotCarryExtraDeclarations(t *testing.T) {
	vm := newJSVM(t)
	got := jsString(t, vm, `renderTool("blog_list", {posts:[{id:"a",title:"x",status:"#000;position:fixed;inset:0;opacity:0"}]}).model.rows[0].badge`)
	if strings.Contains(got, "position:fixed") {
		t.Errorf("a status token injected extra CSS declarations: %q", got)
	}
}

// TestViewsAreNotReachableThroughObjectPrototype: VIEWS is null-prototype, so a tool named like an inherited member cannot resolve to one.
func TestViewsAreNotReachableThroughObjectPrototype(t *testing.T) {
	vm := newJSVM(t)
	if got := jsString(t, vm, `String(Object.getPrototypeOf(VIEWS))`); got != "null" {
		t.Errorf("VIEWS prototype = %s, want null — a tool named like an inherited member would resolve to it", got)
	}
	for _, name := range []string{"toString", "constructor", "valueOf", "hasOwnProperty", "isPrototypeOf"} {
		if got := jsString(t, vm, jsQuote(name)+` in VIEWS ? "yes" : "no"`); got != "no" {
			t.Errorf("%q is reachable in VIEWS through the prototype chain", name)
		}
	}
	for _, name := range []string{"toString", "constructor", "valueOf", "__proto__", "hasOwnProperty"} {
		if got := jsString(t, vm, `String(renderTool(`+jsQuote(name)+`, {}).missing)`); got != "true" {
			t.Errorf("renderTool(%q) resolved to an inherited value instead of the fallback", name)
		}
		if got := jsString(t, vm, `String(renderTool(`+jsQuote(name)+`, {}).template)`); got != "null" {
			t.Errorf("renderTool(%q) produced a template from an inherited value: %s", name, got)
		}
	}
}

func TestBundleExportsNoGlobalDispatchSeam(t *testing.T) {
	src := mustRead(t, "src/boot.js")
	for _, bad := range []string{"globalThis.__toolInput", "globalThis.__dispatchAction"} {
		if strings.Contains(src, bad) {
			t.Errorf("%s ships in the served bundle; any script execution in the frame reaches it and can dispatch a write", bad)
		}
	}
}

// TestActionsAreNotReachableThroughObjectPrototype: the dispatcher looks an action id straight up in this map, so an id naming an inherited member must resolve to nothing.
func TestActionsAreNotReachableThroughObjectPrototype(t *testing.T) {
	vm := newJSVM(t)
	for _, tc := range []struct{ name, expr string }{
		{"registered view", `renderTool("blog_list", ` + fixtureJSON(t, "blog_list.json") + `).actions`},
		{"missing view", `renderTool("todo_list", {}).actions`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := jsString(t, vm, `String(Object.getPrototypeOf(`+tc.expr+`))`); got != "null" {
				t.Errorf("actions prototype = %s, want null", got)
			}
			for _, id := range []string{"toString", "constructor", "valueOf", "hasOwnProperty", "isPrototypeOf"} {
				if got := jsString(t, vm, `String(`+tc.expr+`[`+jsQuote(id)+`])`); got != "undefined" {
					t.Errorf("action id %q resolved to an inherited value: %s", id, got)
				}
			}
		})
	}
}

// TestAViewWithoutACallableModelIsReportedMissing: registerView stores whatever a fork hands it, so the render path checks the model is callable, not merely present.
func TestAViewWithoutACallableModelIsReportedMissing(t *testing.T) {
	for _, tc := range []struct{ name, model string }{
		{"undefined model", "undefined"},
		{"object model", `{ __raw: "<img src=x onerror=alert(1)>" }`},
		{"string model", `"blogListModel"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm := newJSVM(t)
			if _, err := vm.RunString(`registerView("half_wired", ` + tc.model + `, function () { return "template"; });`); err != nil {
				t.Fatalf("register: %v", err)
			}
			if got := jsString(t, vm, `String(renderTool("half_wired", {}).missing)`); got != "true" {
				t.Errorf("a view with a %s reported missing = %s, want true", tc.name, got)
			}
			if got := jsString(t, vm, `String(renderTool("half_wired", {}).template)`); got != "null" {
				t.Errorf("a view with a %s still produced a template: %s", tc.name, got)
			}
		})
	}
}
