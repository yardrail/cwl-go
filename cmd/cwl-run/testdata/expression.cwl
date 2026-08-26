cwlVersion: v1.2
class: ExpressionTool
requirements:
  InlineJavascriptRequirement: {}
inputs:
  message:
    type: string
    default: hello
outputs:
  greeting: string
  count: int
expression: |
  ${ return {"greeting": inputs.message, "count": inputs.message.length}; }
