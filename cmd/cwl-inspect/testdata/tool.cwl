cwlVersion: v1.2
class: CommandLineTool
id: "#echo"
label: echo a message
doc: Writes its message to standard output.
baseCommand: [echo, "-n"]
arguments:
  - "--literal"
  - prefix: "--tagged"
    position: 3
    separate: false
stdout: out.txt
successCodes: [0, 7]
requirements:
  - class: InlineJavascriptRequirement
    expressionLib: ["function twice(x) { return x * 2; }"]
  - class: ResourceRequirement
    coresMin: 2
    outdirMin: "$(inputs.threads)"
hints:
  - class: DockerRequirement
    dockerPull: "alpine:3"
inputs:
  - id: message
    type: string
    default: hello
    inputBinding:
      position: 1
      prefix: "--msg"
  - id: reference
    type: ["null", File]
    secondaryFiles:
      - pattern: .fai
        required: true
    loadContents: true
outputs:
  - id: report
    type: stdout
  - id: log
    type: File
    outputBinding:
      glob: ["*.log"]
      outputEval: "$(self[0])"
