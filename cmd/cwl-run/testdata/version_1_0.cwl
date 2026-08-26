cwlVersion: v1.0
class: ExpressionTool
requirements:
  InlineJavascriptRequirement: {}
inputs: []
outputs:
  out: string
expression: '${ return {"out": "hi"}; }'
