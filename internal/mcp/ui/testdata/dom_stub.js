// NOTE: enough DOM for boot.js only — the Lit render layer is covered by render_browser_test.go, not by this stub.
(function () {
  function El(tag, attrs) {
    this.tagName = tag || "div";
    this.attrs = Object.assign(Object.create(null), attrs || {});
    this.children = [];
    this.disabled = false;
  }
  El.prototype.getAttribute = function (k) {
    return k in this.attrs ? this.attrs[k] : null;
  };
  El.prototype.matches = function (sel) {
    if (sel === "[name]") return this.attrs["name"] !== undefined;
    if (sel === "form") return this.tagName === "form";
    return false;
  };
  El.prototype.querySelectorAll = function (sel) {
    const out = [];
    for (let i = 0; i < this.children.length; i++) {
      if (this.children[i].matches(sel)) out.push(this.children[i]);
    }
    return out;
  };
  El.prototype.querySelector = function (sel) {
    const all = this.querySelectorAll(sel);
    return all.length ? all[0] : null;
  };
  El.prototype.closest = function (sel) {
    let n = this;
    while (n) {
      if (n.matches(sel)) return n;
      n = n.parent || null;
    }
    return null;
  };

  const root = new El("div", { id: "root" });
  root.appendChild = function (child) { this.children.push(child); child.parent = this; };

  globalThis.__root = root;
  globalThis.__mkEl = function (attrs, children, tag) {
    const e = new El(tag || "button", attrs);
    e.children = children || [];
    for (let i = 0; i < e.children.length; i++) e.children[i].parent = e;
    return e;
  };
  globalThis.console = globalThis.console || { error: function () {}, log: function () {} };
  globalThis.document = {
    getElementById: function (id) {
      return id === "root" ? root : null;
    },
    createElement: function (tag) {
      const e = new El(tag);
      e.status = "";
      e.message = "";
      e.detail = "";
      e.view = null;
      e.onaction = null;
      if (tag === "opensheet-app") globalThis.__appEl = e;
      return e;
    },
  };
})();
