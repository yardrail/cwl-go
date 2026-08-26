cwlVersion: v1.2
class: CommandLineTool
id: "#echo"
label: echo a message
doc:
  - Writes its message to standard output.
  - A second paragraph.
intent:
  - http://edamontology.org/operation_0004
baseCommand: [echo, "-n"]
arguments:
  - "--literal"
  - "$(inputs.message)"
  - prefix: "--tagged"
    position: 3
    separate: false
    shellQuote: false
stdin: in.txt
stdout: out.txt
stderr: err.txt
successCodes: [0, 7]
temporaryFailCodes: [3]
permanentFailCodes: [1]
requirements:
  - class: InlineJavascriptRequirement
    expressionLib: ["function twice(x) { return x * 2; }"]
  - class: ResourceRequirement
    coresMin: 2
    ramMin: 1.5
    outdirMin: "$(inputs.threads)"
  - class: EnvVarRequirement
    envDef:
      PATH: /usr/bin
      HOME: "$(runtime.outdir)"
hints:
  - class: DockerRequirement
    dockerPull: "alpine:3"
inputs:
  - id: message
    type: string
    inputBinding:
      position: 1
      prefix: "--msg"
  - id: reference
    type: ["null", File]
    format: ["http://edamontology.org/format_1929"]
    secondaryFiles:
      - pattern: .fai
        required: true
    loadContents: true
    loadListing: deep_listing
    streamable: true
outputs:
  - id: report
    type: stdout
  - id: log
    type: File
    outputBinding:
      glob: ["*.log"]
      outputEval: "$(self[0])"
      loadContents: true
      loadListing: shallow_listing
