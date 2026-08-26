cwlVersion: v1.2
class: Workflow
inputs: []
outputs:
  result:
    type: string
    outputSource: legacy/out
steps:
  legacy:
    run: version_1_0.cwl
    in: []
    out: [out]
