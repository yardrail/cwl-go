cwlVersion: v1.2
class: Workflow
inputs:
  messages:
    type: string[]
    default: [a, b]
outputs:
  result:
    type: string[]
    outputSource: shout/out
steps:
  shout:
    scatter: text
    in:
      text: messages
    out: [out]
    run:
      class: ExpressionTool
      requirements:
        InlineJavascriptRequirement: {}
      inputs:
        text: string
      outputs:
        out: string
      expression: '${ return {"out": inputs.text}; }'
