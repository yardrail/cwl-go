cwlVersion: v1.2
class: CommandLineTool
id: "#plain"
label: a tool with no expressions
doc: Concatenates its inputs.
baseCommand: [cat]
requirements:
  - class: ResourceRequirement
    coresMin: 2
    ramMin: 1.5
  - class: EnvVarRequirement
    envDef:
      PATH: /usr/bin
hints:
  - class: DockerRequirement
    dockerPull: "alpine:3"
successCodes: [0, 7]
inputs:
  - id: files
    type:
      type: array
      items: File
    inputBinding:
      position: 1
      separate: false
outputs:
  - id: report
    type: stdout
stdout: out.txt
