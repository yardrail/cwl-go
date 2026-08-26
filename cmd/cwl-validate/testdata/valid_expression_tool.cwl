cwlVersion: v1.2
class: ExpressionTool
id: "#pass"
expression: "${return {out: inputs.x};}"
inputs:
  x: string
outputs:
  out: string
