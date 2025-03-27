const removePrefix = (value, prefix) =>
    value.startsWith(prefix) ? value.slice(prefix.length) : value;

function search(url, cursor, exhibition, ki, sortField, sortOrder) {
    let search = document.getElementById("search").value;

    const params = new URLSearchParams({
        search: search,
    });
    if (cursor !== undefined && cursor !== "") {
        params.set("cursor", cursor);
    }

    if (exhibition !== undefined && exhibition) {
        params.set("exhibition", "");
    }

    if (ki !== undefined && ki) {
        params.set("ki", "");
    }

    let colls = document.getElementsByClassName("collectionButton")
    let collParam = "";
    for (let i = 0; i < colls.length; i++) {
        if (colls[i].getAttribute("selected") === "true") {
            collParam += colls[i].getAttribute("value") + ",";
        }
    }
    if ( colls.length == 0 ){
        colls = document.getElementsByClassName("collectionCheck");
        for (let i = 0; i < colls.length; i++) {
            if (colls[i].checked) {
                collParam += colls[i].getAttribute("value") + ",";
            }
        }
    }
    if (collParam !== "") {
        params.set("collections", collParam);
    }



    let vocs = document.getElementsByClassName("vocButton")
    let vocParam = "";
    for (let i = 0; i < vocs.length; i++) {
        if (vocs[i].getAttribute("selected") === "true") {
            let value = vocs[i].getAttribute("value");
            value = removePrefix(value, "voc:generic:");
            vocParam += value + ",";
        }
    }
    if ( vocs.length > 0 ){
        params.set("vocabulary", vocParam);
    }
    if (sortField !== undefined && sortField !== "") {
        params.set("sortField", sortField);
        if (sortOrder !== undefined && sortOrder !== "") {
            params.set("sortOrder", sortOrder);
        }
    }
    window.location.href = url + "?" + params.toString();
}