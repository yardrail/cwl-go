cwlVersion: v1.2
$namespaces:
  ext: "https://example.com/ext#"
$graph:
  - id: "#main"
    class: Workflow
    cwlVersion: v1.2
    inputs:
      message:
        type: string
    outputs:
      post_ts:
        type: string
        outputSource: send/ts
    steps:
      send:
        run:
          class: ext:ConnectorAction
          capability: "https://example.com/traits#send_message"
          inputs:
            text: string
          outputs:
            ts: string
        in:
          text: message
        out: [ts]
