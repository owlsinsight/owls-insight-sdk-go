package owls

// Version is the SDK version. It is part of the label the SDK identifies itself
// with (see [SDKLabel]).
const Version = "0.1.2"

// SDKLabel is sent as the User-Agent of every request and as auth.sdk in the
// WebSocket handshake: owls-insight-go/<Version>. The server keeps printable ASCII
// up to 48 characters of it.
const SDKLabel = "owls-insight-go/" + Version
