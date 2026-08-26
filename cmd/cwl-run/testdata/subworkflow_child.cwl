cwlVersion: v1.2
class: Workflow
inputs: []
outputs:
  out:
    type: File
    outputSource: inner/out
steps:
  inner:
    run: echo.cwl
    in: []
    out: [out]
