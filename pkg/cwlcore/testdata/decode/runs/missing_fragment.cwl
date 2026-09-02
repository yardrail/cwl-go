cwlVersion: v1.2
class: Workflow
inputs:
  message: string
outputs: []
steps:
  orphan:
    run: tool.cwl#no-such-id
    in:
      message: message
    out: []
