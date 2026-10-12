// NOTE: null-prototype, so a tool or action id named like an Object.prototype member cannot resolve to an inherited value.
const VIEWS = Object.create(null);

function registerView(name, model, template) {
  VIEWS[name] = { model: model, template: template };
}

function renderTool(name, data) {
  const view = VIEWS[name];
  if (!view || typeof view.model !== "function") {
    return { tool: name, missing: true, model: null, template: null, actions: Object.create(null) };
  }
  const actions = Object.create(null);
  const model = view.model(data || {}, function action(id, tool, args) {
    actions[id] = { tool: tool, args: args || {} };
    return id;
  });
  return { tool: name, missing: false, model: model, template: view.template, actions: actions };
}
