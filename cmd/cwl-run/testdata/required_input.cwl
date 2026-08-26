cwlVersion: v1.2
class: ExpressionTool
requirements:
  InlineJavascriptRequirement: {}
inputs:
  needed: string
outputs:
  out: string
expression: '${ return {"out": inputs.needed}; }'
