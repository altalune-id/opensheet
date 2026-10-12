function createBridge(handlers, loadModule) {
  let app = null;
  let mod = null;

  function applyHostStyles(ctx) {
    if (!ctx || !mod) return;
    if (ctx.theme) mod.applyDocumentTheme(ctx.theme);
    if (ctx.styles && ctx.styles.variables) mod.applyHostStyleVariables(ctx.styles.variables);
    if (ctx.styles && ctx.styles.css && ctx.styles.css.fonts) mod.applyHostFonts(ctx.styles.css.fonts);
  }

  async function connect() {
    mod = await loadModule();
    // NOTE: capabilities is the SECOND positional argument; omitting the third keeps autoResize on.
    app = new mod.App({ name: "opensheet", version: "1.0.0" }, { availableDisplayModes: ["inline"] });
    app.ontoolinput = function (params) {
      const ctx = app.getHostContext();
      const tool = ctx && ctx.toolInfo && ctx.toolInfo.tool ? ctx.toolInfo.tool.name : "";
      handlers.onToolInput(params, tool);
    };
    app.ontoolresult = function (result) { handlers.onToolResult(result); };
    app.ontoolcancelled = function (params) { handlers.onToolCancelled(params); };
    app.onhostcontextchanged = function (ctx) { applyHostStyles(ctx); handlers.onHostContext(ctx); };
    app.onerror = function (e) { console.error(e); };
    // NOTE: the SDK registers the ui/resource-teardown handler only inside this setter.
    app.onteardown = function () { return {}; };
    await app.connect();
    applyHostStyles(app.getHostContext());
    return app.getHostContext();
  }

  return {
    connect: connect,
    hostContext: function () { return app ? app.getHostContext() : null; },
    callTool: function (name, args) {
      if (!app) return Promise.reject(new Error("bridge not connected"));
      return app.callServerTool({ name: name, arguments: args || {} });
    },
  };
}
