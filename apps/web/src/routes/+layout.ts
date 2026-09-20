// The host app is a client-side SPA: the HttpOnly session cookie belongs to the browser, so every
// API call is made from the browser rather than during server rendering.
export const ssr = false;
