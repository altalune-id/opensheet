const app = document.createElement("opensheet-app");
document.getElementById("root").appendChild(app);

let current = { actions: Object.create(null) };
let inflight = null;
let lastCall = null;
let lastToolInfo = "";

// SECURITY: ui/notifications/tool-input carries no tool name or call id, so hostInput is never read once answers already names the tool.
let answers = { tool: "", args: {} };
let hostInput = { tool: "", args: {} };

function capturedArgs() { return answers.args; }

// NOTE: hostContext names the tool that instantiated the view, never the tool a view-initiated call ran.
function currentToolName(b) {
  const ctx = b.hostContext();
  return ctx && ctx.toolInfo && ctx.toolInfo.tool ? ctx.toolInfo.tool.name : "";
}

function toolInfoKey(ctx) {
  const info = ctx && ctx.toolInfo;
  if (!info || !info.tool) return "";
  return String(info.id === undefined ? "" : info.id) + "|" + info.tool.name;
}

function notice(message, detail) {
  app.view = null;
  app.message = message;
  app.detail = detail || "";
  app.status = "notice";
}

function fail(message) {
  app.view = null;
  app.message = message;
  app.status = "error";
}

function paint(name, data) {
  if (!name) {
    current = { actions: Object.create(null) };
    notice("The host sent a result with no tool name.", "");
    return;
  }
  current = renderTool(name, data || {});
  app.view = current;
  app.status = "view";
}

function paintCancelled(reason) {
  if (lastCall) lastCall.done = true;
  inflight = null;
  current = { actions: Object.create(null) };
  notice("The host cancelled that tool call.", reason);
}

function paintResult(name, result) {
  if (result && result.isError) {
    current = { actions: Object.create(null) };
    fail("That tool call failed.");
    return;
  }
  paint(name, (result || {}).structuredContent || {});
}

// SECURITY: a field name comes from tool output, so a segment reaching Object.prototype would poison every later tool call's arguments.
function unsafeKey(k) {
  return k === "__proto__" || k === "constructor" || k === "prototype";
}

// NOTE: protojson silently discards a flat "target.org", so a dotted field name expands into the nested object the request expects.
function setIn(obj, path, value) {
  const parts = String(path).split(".");
  for (let i = 0; i < parts.length; i++) {
    if (unsafeKey(parts[i])) return;
  }
  let node = obj;
  for (let i = 0; i < parts.length - 1; i++) {
    const k = parts[i];
    if (!node[k] || typeof node[k] !== "object") node[k] = {};
    node = node[k];
  }
  node[parts[parts.length - 1]] = value;
}

function formValues(form) {
  const out = {};
  if (!form) return out;
  const fields = form.querySelectorAll("[name]");
  for (let i = 0; i < fields.length; i++) {
    const name = fields[i].getAttribute("name");
    if (name) setIn(out, name, fields[i].value);
  }
  return out;
}

// SECURITY: Object.keys, never for-in — for-in would also read whatever a polluted prototype carries.
function mergeDeep(target, source) {
  const keys = Object.keys(source || {});
  for (let i = 0; i < keys.length; i++) {
    const k = keys[i];
    if (unsafeKey(k)) continue;
    const v = source[k];
    if (v && typeof v === "object" && !Array.isArray(v)) {
      if (!target[k] || typeof target[k] !== "object") target[k] = {};
      mergeDeep(target[k], v);
      continue;
    }
    target[k] = v;
  }
  return target;
}

const hostHandlers = {
  onToolInput: function (params, tool) {
    hostInput = { tool: tool || "", args: (params && params.arguments) || {} };
  },
  onToolResult: function (result) {
    if (!lastCall) {
      paintResult(currentToolName(bridge), result);
      return;
    }
    if (lastCall.done) {
      lastCall = null;
      return;
    }
    lastCall.done = true;
    paintResult(lastCall.tool, result);
  },
  onToolCancelled: function (params) {
    if (lastCall && lastCall.done) return;
    paintCancelled(params && params.reason);
  },
  onHostContext: function (ctx) {
    const key = toolInfoKey(ctx);
    if (!key || key === lastToolInfo) return;
    lastToolInfo = key;
    lastCall = null;
    answers = { tool: "", args: {} };
    hostInput = { tool: "", args: {} };
  },
};

const bridge = createBridge(hostHandlers, function () { return globalThis.__extApps; });

// SECURITY: a.args is merged LAST, so a form field can never override a declared argument.
function dispatchAction(id, el, form) {
  if (inflight) return;
  const a = current.actions[id];
  if (!a) return;
  delete current.actions[id];

  const args = {};
  if (answers.tool === a.tool) mergeDeep(args, answers.args);
  else if (hostInput.tool === a.tool) mergeDeep(args, hostInput.args);
  mergeDeep(args, formValues(form));
  mergeDeep(args, a.args);
  answers = { tool: a.tool, args: args };

  const call = { tool: a.tool, done: false };
  inflight = call;
  lastCall = call;
  if (el) el.disabled = true;
  bridge.callTool(a.tool, args)
    .then(function (res) {
      if (call.done) {
        if (lastCall === call) lastCall = null;
        return;
      }
      call.done = true;
      paintResult(call.tool, res);
    })
    .catch(function (e) {
      if (call.done) return;
      call.done = true;
      current = { actions: Object.create(null) };
      fail("That request was not completed.");
      console.error(e);
    })
    .finally(function () {
      if (inflight === call) inflight = null;
    });
}

app.onaction = function (d) { dispatchAction(d.id, d.el, d.form); };

bridge.connect().then(function (ctx) {
  lastToolInfo = toolInfoKey(ctx);
}).catch(function (e) {
  fail("Could not reach the host.");
  console.error(e);
});
