function postRow(p, action) {
  const status = p.status || "draft";
  return {
    id: text(p.id, ""),
    badge: badgeColour(status),
    title: text(p.title, "Untitled"),
    category: text((p.category || {}).name, "—"),
    tags: (p.tags || []).map(function (t) { return text(t.name, ""); }).join(" · "),
    day: day(p.firstPublishedAt),
    publish: status === "published" ? "" : action("publish:" + text(p.id, ""), "blog_publish", { postId: p.id }),
  };
}

function blogListModel(d, action) {
  const posts = d.posts || [];
  const published = posts.filter(function (p) { return p.status === "published"; }).length;
  return {
    empty: posts.length === 0,
    kpis: [
      { label: "Posts", value: num(posts.length) },
      { label: "Published", value: num(published) },
      { label: "Drafts", value: num(posts.length - published) },
    ],
    rows: posts.map(function (p) { return postRow(p, action); }),
  };
}
