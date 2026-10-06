// The export keeps its files in two trees: assets/ (scripts, styles, fonts) and api/ (each page's
// pre-rendered data). Neither holds HTML, so an HTML answer for a path under one is the
// single-page-application fallback standing in for a file that does not exist. Answer that with a
// 404. Otherwise a missing script gets the app's index.html with a 200: the browser refuses it on
// its MIME type and shows a blank page, while curl and link checkers see success. That is how
// /guides/macos went blank (2026-10-05): its relative ./assets/ URLs asked for /guides/assets/.
// Matched at any depth, because a relative URL resolved below the root is that request.
const FILE_TREES = /(^|\/)(assets|api)\//;

export default {
  async fetch(request, env) {
    const response = await env.ASSETS.fetch(request);
    const type = response.headers.get("content-type") ?? "";
    if (FILE_TREES.test(new URL(request.url).pathname) && type.startsWith("text/html")) {
      return new Response("Not found\n", {
        status: 404,
        headers: { "content-type": "text/plain; charset=utf-8" },
      });
    }
    return response;
  },
};
