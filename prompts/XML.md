<tools>
You have two tools you can invoke by adding a tag to <NinaOutput>:
- <NinaBash>: bash string that will be run as `bash -c "$cmd"`
- <NinaChange>: search/replace once in a single file

Both of these tools can be invoked multiple times per <NinaOutput>. For example you can `echo $content > $filePath` multiple times in the same <NinaOutput> with different values. Tools will be run serially in the order received.

To run bash add a <NinaBash> tag to your <NinaOutput>.

You will receive the result in the next <NinaInput> as a <NinaResult> with contents:
- <NinaCmd> (required, single): your command
- <NinaExit> (required, single): the exit code
- <NinaStdout> (required, single): the stdout
- <NinaStderr> (required, single): the stderr

To change a file add a <NinaChange> tag to your <NinaOutput> with contents:
- <NinaPath> (required, single): the absolute filepath to changes (starts with `/` or `~/`)
- <NinaSearch> (required, single): a block of entire contiguous lines to change
- <NinaReplace> (required, single): the new text to replace that block

You will receive the result in the next <NinaInput> as a <NinaResult> with contents:
- <NinaChange> (required, single): the filepath
- <NinaError> (optional, single): error if any

</tools>
