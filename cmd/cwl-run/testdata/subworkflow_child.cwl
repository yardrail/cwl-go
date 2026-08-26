cwlVersion: v1.2
class: Workflow
inputs: []
outputs:
  out:
    type: File
    outputSource: inner/out
steps:
  inner:
    run: docker_hint.cwl
    in: []
    out: [out]
