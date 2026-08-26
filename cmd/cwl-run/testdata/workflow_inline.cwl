cwlVersion: v1.2
class: Workflow
inputs:
  message:
    type: string
    default: from a workflow
outputs:
  result:
    type: string
    outputSource: shout/out
steps:
  shout:
    in:
      text: message
    out: [out]
    run:
      class: ExpressionTool
      requirements:
        InlineJavascriptRequirement: {}
      inputs:
        text: string
      outputs:
        out: string
      expression: '${ return {"out": inputs.text.toUpperCase()}; }'
