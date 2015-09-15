# zipsaver
A tool to restore files from a zip with "broken" or missing central directory.

    usage:
        zipsave [-v] [--override] [--out output.zip] input.zip

The purpose of this tool is to "recover" files from a zip archive with "broken" or missing central directory.

It is useful to check zip files created by long running processes either when the process is running (the central directory will be written only when the process terminates) or the process is aborted (leaving the zip file corrupted).

This tool will try to extract files out of a zip archive without looking at the central directory, but parsing
the file top to bottom.

It works by decoding the file header (to get file name and possibly file length), decoding the compressed payload
(that most of the time can be done without knowing the compressed size) and then decoding the additional header
(to get correct compressed and uncompressed sizes).

The process stops at the first error (note that when generating a new zip file the last file will probably be empty,
since the header has already been created and cannot be removed)

Note that "stored" files can only be extracted if the size is correctly set in the "file" header (we can't get to the "additional header" until we read the file). The process could be improved by "searching" for the next header while reading the file payload.
