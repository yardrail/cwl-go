cwlVersion: v1.2
class: ExpressionTool
requirements:
  InlineJavascriptRequirement: {}
inputs:
  a_double:
    type: double
    default: 1000000000000000000000000000000000000000000
  a_small:
    type: double
    default: 0.00001
outputs:
  out_double: double
  out_small: double
expression: "${return {out_double: inputs.a_double, out_small: inputs.a_small};}"
