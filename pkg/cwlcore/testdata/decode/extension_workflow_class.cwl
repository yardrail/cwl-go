cwlVersion: v1.2
$namespaces:
  ext: "https://example.com/ext#"
class: ext:ExtWorkflow
inputs:
  message:
    type: string
outputs:
  out:
    type: string
    outputSource: echo/out
steps:
  echo:
    run:
      class: ExpressionTool
      requirements:
        InlineJavascriptRequirement: {}
      inputs:
        message: string
      outputs:
        out: string
      expression: '${return {"out": inputs.message};}'
    in:
      message: message
    out: [out]
