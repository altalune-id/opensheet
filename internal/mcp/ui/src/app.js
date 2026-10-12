class OpensheetApp extends LitElement {
  static properties = { status: {}, message: {}, detail: {}, view: { attribute: false } };
  static styles = appStyles;

  constructor() {
    super();
    this.status = "notice";
    this.message = "Loading…";
    this.detail = "";
    this.view = null;
    this.onaction = null;
  }

  firstUpdated() {
    this.renderRoot.addEventListener("opensheet-action", (ev) => {
      ev.stopPropagation();
      if (this.onaction) this.onaction(ev.detail);
    });
    this.renderRoot.addEventListener("submit", (ev) => {
      if (ev.preventDefault) ev.preventDefault();
    });
  }

  render() {
    if (this.status === "error") return html`<p class="app-error">${this.message}</p>`;
    if (this.status !== "view") {
      return html`<p class="app-muted">${this.message}</p>
        ${this.detail ? html`<p class="app-muted">${this.detail}</p>` : nothing}`;
    }
    const v = this.view;
    if (!v || v.missing || typeof v.template !== "function") {
      return html`<p class="app-muted">No view for "${v ? v.tool : ""}".</p>`;
    }
    return v.template(v.model);
  }
}

customElements.define("opensheet-app", OpensheetApp);
