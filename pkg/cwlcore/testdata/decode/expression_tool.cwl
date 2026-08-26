cwlVersion: v1.2
class: ExpressionTool
id: "#pick"
expression: "${return {picked: inputs.candidates[0]};}"
inputs:
  candidates:
    type:
      type: array
      items: string
    default: [a, b]
outputs:
  picked: string
