cwlVersion: v1.2
$graph:
  - id: "#tool"
    class: CommandLineTool
    cwlVersion: v1.2
    baseCommand: [wc, "-l"]
    inputs:
      files:
        type:
          type: array
          items: File
      threads: int
    outputs:
      out: stdout
    stdout: counted.txt
  - id: "#main"
    class: Workflow
    cwlVersion: v1.2
    label: two step workflow
    requirements:
      ScatterFeatureRequirement: {}
      MultipleInputFeatureRequirement: {}
    inputs:
      files:
        type:
          type: array
          items: File
      threads: int
    outputs:
      merged:
        type: File
        outputSource: [step_two/out, step_one/out]
        linkMerge: merge_flattened
        pickValue: all_non_null
    steps:
      step_one:
        run: "#tool"
        scatter: files
        scatterMethod: dotproduct
        when: "$(inputs.threads > 0)"
        in:
          files: files
          threads:
            source: [threads]
            default: 4
            valueFrom: "$(self)"
            linkMerge: merge_nested
            pickValue: first_non_null
            loadContents: true
        out: [out]
      step_two:
        run:
          class: ExpressionTool
          cwlVersion: v1.2
          expression: "${return {out: inputs.x};}"
          inputs:
            x: File
          outputs:
            out: File
        in:
          x: step_one/out
        out:
          - id: out
        hints:
          - class: http://example.org/ext#Note
            level: info
