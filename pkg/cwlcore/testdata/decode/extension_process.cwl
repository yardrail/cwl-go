cwlVersion: v1.2
$namespaces:
  ext: "https://example.com/ext#"
class: ext:ConnectorAction
capability: "https://example.com/traits#send_message"
inputs:
  text:
    type: string
outputs:
  ts:
    type: string
