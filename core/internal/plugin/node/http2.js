// node:http2 for plugins: the constants and settings helpers work; sessions
// and servers are not available, so that a dependency that only imports the
// module still runs.
(function (module, exports, require) {
  "use strict";
  const { unavailable } = require("internal/errors");
  const reason = "plugins cannot open sockets or servers; use fetch, node:http or node:https to reach the network";
  const denied = (what) => () => {
    throw unavailable(`http2.${what}`, reason);
  };
  const constants = {
    NGHTTP2_SESSION_SERVER: 0, NGHTTP2_SESSION_CLIENT: 1, NGHTTP2_NO_ERROR: 0, NGHTTP2_PROTOCOL_ERROR: 1,
    NGHTTP2_INTERNAL_ERROR: 2, NGHTTP2_FLOW_CONTROL_ERROR: 3, NGHTTP2_SETTINGS_TIMEOUT: 4,
    NGHTTP2_STREAM_CLOSED: 5, NGHTTP2_FRAME_SIZE_ERROR: 6, NGHTTP2_REFUSED_STREAM: 7, NGHTTP2_CANCEL: 8,
    NGHTTP2_COMPRESSION_ERROR: 9, NGHTTP2_CONNECT_ERROR: 10, NGHTTP2_ENHANCE_YOUR_CALM: 11,
    NGHTTP2_INADEQUATE_SECURITY: 12, NGHTTP2_HTTP_1_1_REQUIRED: 13, NGHTTP2_DEFAULT_WEIGHT: 16,
    HTTP2_HEADER_STATUS: ":status", HTTP2_HEADER_METHOD: ":method", HTTP2_HEADER_AUTHORITY: ":authority",
    HTTP2_HEADER_SCHEME: ":scheme", HTTP2_HEADER_PATH: ":path", HTTP2_HEADER_PROTOCOL: ":protocol",
    HTTP2_HEADER_ACCEPT: "accept", HTTP2_HEADER_ACCEPT_ENCODING: "accept-encoding",
    HTTP2_HEADER_AUTHORIZATION: "authorization", HTTP2_HEADER_CONTENT_ENCODING: "content-encoding",
    HTTP2_HEADER_CONTENT_LENGTH: "content-length", HTTP2_HEADER_CONTENT_TYPE: "content-type",
    HTTP2_HEADER_COOKIE: "cookie", HTTP2_HEADER_DATE: "date", HTTP2_HEADER_ETAG: "etag",
    HTTP2_HEADER_LOCATION: "location", HTTP2_HEADER_SET_COOKIE: "set-cookie",
    HTTP2_HEADER_USER_AGENT: "user-agent", HTTP2_HEADER_TE: "te",
    HTTP2_METHOD_GET: "GET", HTTP2_METHOD_POST: "POST", HTTP2_METHOD_PUT: "PUT",
    HTTP2_METHOD_DELETE: "DELETE", HTTP2_METHOD_PATCH: "PATCH", HTTP2_METHOD_HEAD: "HEAD",
    HTTP2_METHOD_OPTIONS: "OPTIONS", HTTP_STATUS_OK: 200, HTTP_STATUS_NO_CONTENT: 204,
    HTTP_STATUS_BAD_REQUEST: 400, HTTP_STATUS_NOT_FOUND: 404, HTTP_STATUS_INTERNAL_SERVER_ERROR: 500,
  };
  module.exports = {
    constants,
    sensitiveHeaders: Symbol("nodejs.http2.sensitiveHeaders"),
    connect: denied("connect()"),
    createServer: denied("createServer()"),
    createSecureServer: denied("createSecureServer()"),
    getDefaultSettings: () => ({ headerTableSize: 4096, enablePush: true, initialWindowSize: 65535, maxFrameSize: 16384, maxConcurrentStreams: 4294967295, maxHeaderListSize: 65535, enableConnectProtocol: false }),
    getPackedSettings: () => require("buffer").Buffer.alloc(0),
    getUnpackedSettings: () => ({}),
  };
})
