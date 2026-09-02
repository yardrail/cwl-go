cwlVersion: v1.2
class: Workflow
inputs: []
outputs:
  result:
    type: string
    outputSource: s1
steps:
  s1:
    in: []
    out: [out]
    run:
      class: ExpressionTool
      inputs: []
      outputs:
        out: string
      expression: '${ return {"out": "x"}; }'
